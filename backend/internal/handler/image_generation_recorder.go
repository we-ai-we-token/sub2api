package handler

import (
	"context"
	"strings"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/pkg/ctxkey"
	"github.com/Wei-Shaw/sub2api/internal/pkg/logger"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
	"go.uber.org/zap"
)

// imageGenerationRecordState 在 images handler 生命周期内累积生图记录所需的状态，
// 请求结束时（defer）由 finishImageGenerationRecord 统一落库，成功与失败都记一行。
// 分段耗时不在这里采集：全部复用请求 context 上已有的 ops latency 埋点。
type imageGenerationRecordState struct {
	startedAt time.Time
	platform  string
	endpoint  string
	model     string
	stream    bool

	apiKey *service.APIKey

	forwardAttempts    int
	accountSwitches    int
	sameAccountRetries int

	lastAccount *service.Account
	succeeded   bool

	// 成功结果的摘要字段（由 noteSuccess* 从各链路的 result 提取，屏蔽结果类型差异）
	upstreamModel     string
	imageCount        int
	imageSize         string
	imageQuality      string
	upstreamRequestID string
}

func newImageGenerationRecordState(c *gin.Context, apiKey *service.APIKey, platform, model string, stream bool, startedAt time.Time) *imageGenerationRecordState {
	endpoint := "generations"
	if c != nil && c.Request != nil && c.Request.URL != nil && strings.Contains(c.Request.URL.Path, "/edits") {
		endpoint = "edits"
	}
	return &imageGenerationRecordState{
		startedAt: startedAt,
		platform:  platform,
		endpoint:  endpoint,
		model:     model,
		stream:    stream,
		apiKey:    apiKey,
	}
}

// noteAttempt 在每次发起上游转发前调用。
func (st *imageGenerationRecordState) noteAttempt(account *service.Account) {
	if st == nil {
		return
	}
	st.forwardAttempts++
	if account != nil {
		st.lastAccount = account
	}
}

// noteSuccess 在 OpenAI 兼容 Images 链路最终成功（进入计费记录）时调用。
func (st *imageGenerationRecordState) noteSuccess(result *service.OpenAIForwardResult) {
	if st == nil {
		return
	}
	st.succeeded = true
	if result == nil {
		return
	}
	st.upstreamModel = result.UpstreamModel
	st.imageCount = result.ImageCount
	st.imageSize = result.ImageSize
	st.imageQuality = result.Usage.Quality
	if result.ResponseHeaders != nil {
		st.upstreamRequestID = result.ResponseHeaders.Get("x-request-id")
	}
	if st.upstreamRequestID == "" {
		st.upstreamRequestID = result.RequestID
	}
}

// noteSuccessNative 在 Gemini 原生 generateContent 链路最终成功时调用。
func (st *imageGenerationRecordState) noteSuccessNative(result *service.ForwardResult) {
	if st == nil {
		return
	}
	st.succeeded = true
	if result == nil {
		return
	}
	st.upstreamModel = result.UpstreamModel
	st.imageCount = result.ImageCount
	st.imageSize = result.ImageSize
	st.upstreamRequestID = result.RequestID
}

// finishImageGenerationRecord 组装并异步落一行生图记录。
// 走 usage record worker 池（mandatory 语义），context 已与请求取消解耦。
func (h *OpenAIGatewayHandler) finishImageGenerationRecord(c *gin.Context, st *imageGenerationRecordState) {
	if h == nil || h.imageGenerationRecordService == nil || st == nil || c == nil {
		return
	}
	input := buildImageGenerationRecordInput(c, st)
	svc := h.imageGenerationRecordService
	h.submitMandatoryUsageRecordTask(c.Request.Context(), imageGenerationRecordTask(svc, input))
}

// finishImageGenerationRecord 是 Gemini 原生 generateContent 链路（GatewayHandler）的落库入口。
func (h *GatewayHandler) finishImageGenerationRecord(c *gin.Context, st *imageGenerationRecordState) {
	if h == nil || h.imageGenerationRecordService == nil || st == nil || c == nil {
		return
	}
	input := buildImageGenerationRecordInput(c, st)
	svc := h.imageGenerationRecordService
	h.submitUsageRecordTask(c.Request.Context(), imageGenerationRecordTask(svc, input))
}

func imageGenerationRecordTask(svc *service.ImageGenerationRecordService, input *service.ImageGenerationRecordInput) service.UsageRecordTask {
	return func(ctx context.Context) {
		if err := svc.Record(ctx, input); err != nil {
			logger.L().With(
				zap.String("component", "handler.gateway.image_record"),
				zap.String("platform", input.Platform),
				zap.String("model", input.Model),
			).Warn("image_generation_record_insert_failed", zap.Error(err))
		}
	}
}

func buildImageGenerationRecordInput(c *gin.Context, st *imageGenerationRecordState) *service.ImageGenerationRecordInput {
	input := &service.ImageGenerationRecordInput{
		Platform:           st.platform,
		Endpoint:           st.endpoint,
		Model:              st.model,
		Stream:             st.stream,
		TotalMs:            time.Since(st.startedAt).Milliseconds(),
		Attempts:           st.forwardAttempts,
		AccountSwitches:    st.accountSwitches,
		SameAccountRetries: st.sameAccountRetries,
		Success:            st.succeeded,
		StatusCode:         c.Writer.Status(),
		CreatedAt:          time.Now(),
	}

	input.RequestID = c.Writer.Header().Get("X-Request-Id")
	if input.RequestID == "" {
		input.RequestID = c.Writer.Header().Get("x-request-id")
	}
	if clientRequestID, _ := c.Request.Context().Value(ctxkey.ClientRequestID).(string); clientRequestID != "" {
		input.ClientRequestID = clientRequestID
	}

	if st.apiKey != nil {
		input.APIKeyID = &st.apiKey.ID
		if st.apiKey.User != nil {
			input.UserID = &st.apiKey.User.ID
		}
		if st.apiKey.GroupID != nil {
			input.GroupID = st.apiKey.GroupID
		}
	}
	if st.lastAccount != nil {
		input.AccountID = &st.lastAccount.ID
	}

	input.AuthMs = getContextLatencyMs(c, service.OpsAuthLatencyMsKey)
	input.RoutingMs = getContextLatencyMs(c, service.OpsRoutingLatencyMsKey)
	input.ImageSlotWaitMs = getContextLatencyMs(c, service.OpsImageSlotWaitMsKey)
	input.UserSlotWaitMs = getContextLatencyMs(c, service.OpsUserSlotWaitMsKey)
	input.AccountSlotWaitMs = getContextLatencyMs(c, service.OpsAccountSlotWaitMsKey)
	input.UpstreamMs = getContextLatencyMs(c, service.OpsUpstreamLatencyMsKey)
	input.ResponseMs = getContextLatencyMs(c, service.OpsResponseLatencyMsKey)
	input.FirstTokenMs = getContextLatencyMs(c, service.OpsTimeToFirstTokenMsKey)

	input.UpstreamModel = st.upstreamModel
	input.ImageCount = st.imageCount
	input.ImageSize = st.imageSize
	input.ImageQuality = st.imageQuality
	input.UpstreamRequestID = st.upstreamRequestID

	// 失败的上游尝试明细来自 ops 上游错误事件（最终成功的尝试不在其中）。
	if v, ok := c.Get(service.OpsUpstreamErrorsKey); ok {
		if events, ok := v.([]*service.OpsUpstreamErrorEvent); ok && len(events) > 0 {
			attempts := make([]*service.ImageGenerationAttempt, 0, len(events))
			for _, ev := range events {
				if ev == nil {
					continue
				}
				attempts = append(attempts, &service.ImageGenerationAttempt{
					AtUnixMs:           ev.AtUnixMs,
					AccountID:          ev.AccountID,
					AccountName:        ev.AccountName,
					UpstreamStatusCode: ev.UpstreamStatusCode,
					UpstreamRequestID:  ev.UpstreamRequestID,
					Kind:               ev.Kind,
					Message:            ev.Message,
				})
			}
			input.AttemptsDetail = attempts
			if last := events[len(events)-1]; last != nil && !st.succeeded {
				if last.UpstreamStatusCode > 0 {
					code := last.UpstreamStatusCode
					input.UpstreamStatusCode = &code
				}
				input.UpstreamErrorMessage = last.Message
				if input.UpstreamRequestID == "" {
					input.UpstreamRequestID = last.UpstreamRequestID
				}
			}
		}
	}
	// 单字段上游错误上下文兜底（无 events 时）。
	if !st.succeeded && input.UpstreamStatusCode == nil {
		if v, ok := c.Get(service.OpsUpstreamStatusCodeKey); ok {
			switch t := v.(type) {
			case int:
				if t > 0 {
					code := t
					input.UpstreamStatusCode = &code
				}
			case int64:
				if t > 0 {
					code := int(t)
					input.UpstreamStatusCode = &code
				}
			}
		}
	}
	if !st.succeeded && input.UpstreamErrorMessage == "" {
		if v, ok := c.Get(service.OpsUpstreamErrorMessageKey); ok {
			if s, ok := v.(string); ok {
				input.UpstreamErrorMessage = strings.TrimSpace(s)
			}
		}
	}

	if !st.succeeded {
		hasUpstreamContext := input.UpstreamStatusCode != nil || len(input.AttemptsDetail) > 0
		input.ErrorType = classifyImageGenerationErrorType(c, input.StatusCode, hasUpstreamContext)
	}
	return input
}

// classifyImageGenerationErrorType 粗分类失败原因；深挖细节用 request_id 关联 ops_error_logs。
func classifyImageGenerationErrorType(c *gin.Context, wireStatus int, hasUpstreamContext bool) string {
	if streamErr, ok := service.GetOpsStreamError(c); ok && streamErr.ErrType != "" {
		return streamErr.ErrType
	}
	if isOpsRoutingCapacityLimited(c) {
		return "no_available_account"
	}
	if hasUpstreamContext {
		return "upstream_error"
	}
	switch {
	case wireStatus == 429:
		return "rate_limit_error"
	case wireStatus == 403:
		return "permission_error"
	case wireStatus == 401:
		return "authentication_error"
	case wireStatus >= 500:
		return "api_error"
	case wireStatus >= 400:
		return "invalid_request_error"
	default:
		return "gateway_error"
	}
}
