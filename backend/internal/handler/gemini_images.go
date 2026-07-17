package handler

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/config"
	pkghttputil "github.com/Wei-Shaw/sub2api/internal/pkg/httputil"
	"github.com/Wei-Shaw/sub2api/internal/pkg/ip"
	"github.com/Wei-Shaw/sub2api/internal/pkg/logger"
	middleware2 "github.com/Wei-Shaw/sub2api/internal/server/middleware"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
	"go.uber.org/zap"
)

// GeminiImages 处理 Gemini 分组的 OpenAI Images 请求：
// 请求原样透传到账号 base_url 指向的 OpenAI 兼容上游（仅 AI Studio API Key 账号）。
// 编排结构对齐 handleGrokMedia；v1 仅支持非流式。
func (h *OpenAIGatewayHandler) GeminiImages(c *gin.Context) {
	streamStarted := false
	defer h.recoverResponsesPanic(c, &streamStarted)

	requestStart := time.Now()

	apiKey, ok := middleware2.GetAPIKeyFromContext(c)
	if !ok {
		h.errorResponse(c, http.StatusUnauthorized, "authentication_error", "Invalid API key")
		return
	}
	subject, ok := middleware2.GetAuthSubjectFromContext(c)
	if !ok {
		h.errorResponse(c, http.StatusInternalServerError, "api_error", "User context not found")
		return
	}
	reqLog := requestLogger(
		c,
		"handler.openai_gateway.gemini_images",
		zap.Int64("user_id", subject.UserID),
		zap.Int64("api_key_id", apiKey.ID),
		zap.Any("group_id", apiKey.GroupID),
	)
	if !h.ensureResponsesDependencies(c, reqLog) {
		return
	}
	if h.geminiCompatService == nil {
		h.errorResponse(c, http.StatusInternalServerError, "api_error", "Gemini gateway is not configured")
		return
	}

	body, err := pkghttputil.ReadRequestBodyWithPrealloc(c.Request)
	if err != nil {
		if maxErr, ok := extractMaxBytesError(err); ok {
			h.errorResponse(c, http.StatusRequestEntityTooLarge, "invalid_request_error", buildBodyTooLargeMessage(maxErr.Limit))
			return
		}
		h.errorResponse(c, http.StatusBadRequest, "invalid_request_error", "Failed to read request body")
		return
	}
	if len(body) == 0 {
		h.errorResponse(c, http.StatusBadRequest, "invalid_request_error", "Request body is empty")
		return
	}

	setOpsRequestContext(c, "", false)

	parsed, err := h.gatewayService.ParseOpenAIImagesRequest(c, body)
	if err != nil {
		h.errorResponse(c, http.StatusBadRequest, "invalid_request_error", err.Error())
		return
	}
	if parsed.Stream {
		h.errorResponse(c, http.StatusBadRequest, "invalid_request_error", "stream is not supported for image generation on Gemini groups")
		return
	}
	requestModel := parsed.Model
	reqLog = reqLog.With(
		zap.String("model", requestModel),
		zap.Bool("multipart", parsed.Multipart),
	)

	imageGenRecord := newImageGenerationRecordState(c, apiKey, service.PlatformGemini, requestModel, false, requestStart)
	defer h.finishImageGenerationRecord(c, imageGenRecord)

	if !service.GroupAllowsImageGeneration(apiKey.Group) {
		h.errorResponse(c, http.StatusForbidden, "permission_error", service.ImageGenerationPermissionMessage())
		return
	}
	if decision := h.checkSecurityAudit(c, reqLog, apiKey, subject, service.ContentModerationProtocolOpenAIImages, requestModel, parsed.ModerationBody()); decision != nil && !decision.AllowNextStage {
		h.openAISecurityAuditError(c, decision)
		return
	}
	imageSlotWaitStart := time.Now()
	imageReleaseFunc, acquired := h.acquireImageGenerationSlot(c, streamStarted)
	service.SetOpsLatencyMs(c, service.OpsImageSlotWaitMsKey, time.Since(imageSlotWaitStart).Milliseconds())
	if !acquired {
		return
	}
	if imageReleaseFunc != nil {
		defer imageReleaseFunc()
	}

	setOpsRequestContext(c, requestModel, false)
	setOpsEndpointContext(c, "", int16(service.RequestTypeSync))

	// 分组模型映射（无映射时 MappedModel == 请求模型），随后严格校验映射结果
	channelMapping, _ := h.gatewayService.ResolveChannelMappingAndRestrict(c.Request.Context(), apiKey.GroupID, requestModel)
	mappedModel := channelMapping.MappedModel
	if !service.IsGeminiImageGenerationModel(mappedModel) {
		h.errorResponse(c, http.StatusNotFound, "invalid_request_error",
			fmt.Sprintf("Model %q is not available for image generation on Gemini groups (mapped model %q is not a gemini image model)", requestModel, mappedModel))
		return
	}

	if h.errorPassthroughService != nil {
		service.BindErrorPassthroughService(c, h.errorPassthroughService)
	}

	subscription, _ := middleware2.GetSubscriptionFromContext(c)
	service.SetOpsLatencyMs(c, service.OpsAuthLatencyMsKey, time.Since(requestStart).Milliseconds())
	routingStart := time.Now()

	userReleaseFunc, acquired := h.acquireResponsesUserSlot(c, subject.UserID, subject.Concurrency, false, &streamStarted, reqLog)
	service.SetOpsLatencyMs(c, service.OpsUserSlotWaitMsKey, time.Since(routingStart).Milliseconds())
	if !acquired {
		return
	}
	if userReleaseFunc != nil {
		defer userReleaseFunc()
	}

	if err := h.billingCacheService.CheckBillingEligibility(c.Request.Context(), apiKey.User, apiKey, apiKey.Group, subscription, service.QuotaPlatform(c.Request.Context(), apiKey)); err != nil {
		reqLog.Info("gemini_images.billing_eligibility_check_failed", zap.Error(err))
		status, code, message, retryAfter := billingErrorDetails(err)
		if retryAfter > 0 {
			c.Header("Retry-After", strconv.Itoa(retryAfter))
		}
		h.errorResponse(c, status, code, message)
		return
	}

	requestCtx := service.WithOpenAIImageGenerationIntent(c.Request.Context())

	maxAccountSwitches := h.maxAccountSwitches
	if maxAccountSwitches <= 0 {
		maxAccountSwitches = 3
	}
	switchCount := 0
	failedAccountIDs := make(map[int64]struct{})
	sameAccountRetryCount := make(map[int64]int)
	var lastFailoverErr *service.UpstreamFailoverError
	defer func() {
		imageGenRecord.accountSwitches = switchCount
		for _, n := range sameAccountRetryCount {
			imageGenRecord.sameAccountRetries += n
		}
	}()

	for {
		account, err := h.geminiCompatService.SelectGeminiAPIKeyAccountForImages(requestCtx, apiKey.GroupID, mappedModel, failedAccountIDs)
		if err != nil || account == nil {
			if err != nil {
				reqLog.Warn("gemini_images.account_select_failed",
					zap.Error(err),
					zap.Int("excluded_account_count", len(failedAccountIDs)),
				)
			}
			if len(failedAccountIDs) == 0 {
				// 注：诊断器对非 grok 平台会归一化到 openai，gemini 分组必然走 503 兜底分支；
				// 模型有效性已在前面的映射模型门槛处 404，这里语义正确。
				cls := classifyNoAccountErrorFromGin(c, h.gatewayService, apiKey, mappedModel, requestModel, service.PlatformGemini)
				if !cls.ModelNotFound {
					markOpsRoutingCapacityLimitedIfNoAvailable(c, err)
				}
				h.errorResponse(c, cls.Status, cls.ErrType, cls.Message)
				return
			}
			if lastFailoverErr != nil {
				h.handleFailoverExhausted(c, lastFailoverErr, false)
			} else {
				h.errorResponse(c, http.StatusBadGateway, "api_error", "Upstream request failed")
			}
			return
		}

		sessionHash := ensureOpenAIPoolModeSessionHash("", account)
		setOpsSelectedAccount(c, account.ID, account.Platform)
		reqLog.Debug("gemini_images.account_selected", zap.Int64("account_id", account.ID), zap.String("account_name", account.Name))

		service.SetOpsLatencyMs(c, service.OpsRoutingLatencyMsKey, time.Since(routingStart).Milliseconds())
		forwardStart := time.Now()
		writerSizeBeforeForward := c.Writer.Size()

		selection := buildGeminiImagesAccountSelection(account, h.cfg)
		accountSlotWaitStart := time.Now()
		accountReleaseFunc, acquired := h.acquireResponsesAccountSlot(c, apiKey.GroupID, sessionHash, selection, false, &streamStarted, reqLog)
		service.AddOpsLatencyMs(c, service.OpsAccountSlotWaitMsKey, time.Since(accountSlotWaitStart).Milliseconds())
		if !acquired {
			return
		}
		imageGenRecord.noteAttempt(account)
		result, err := func() (*service.OpenAIForwardResult, error) {
			defer func() {
				if accountReleaseFunc != nil {
					accountReleaseFunc()
				}
			}()
			return h.gatewayService.ForwardGeminiImagesPassthrough(requestCtx, c, account, body, parsed, mappedModel)
		}()
		forwardDurationMs := time.Since(forwardStart).Milliseconds()
		upstreamLatencyMs, _ := getContextInt64(c, service.OpsUpstreamLatencyMsKey)
		responseLatencyMs := forwardDurationMs
		if upstreamLatencyMs > 0 && forwardDurationMs > upstreamLatencyMs {
			responseLatencyMs = forwardDurationMs - upstreamLatencyMs
		}
		service.SetOpsLatencyMs(c, service.OpsResponseLatencyMsKey, responseLatencyMs)

		if err != nil {
			var imageUpstreamErr *service.OpenAIImagesUpstreamError
			if errors.As(err, &imageUpstreamErr) {
				// 用户侧上游错误：响应已由 service 原样写回，不切号
				reqLog.Warn("gemini_images.upstream_user_error",
					zap.Int64("account_id", account.ID),
					zap.Int("status_code", imageUpstreamErr.StatusCode),
					zap.String("error_type", imageUpstreamErr.ErrorType),
				)
				return
			}
			var failoverErr *service.UpstreamFailoverError
			if errors.As(err, &failoverErr) {
				if c.Writer.Size() != writerSizeBeforeForward {
					reqLog.Warn("gemini_images.upstream_failover_skipped_after_flush",
						zap.Int64("account_id", account.ID),
						zap.Int("upstream_status", failoverErr.StatusCode),
					)
					h.handleFailoverExhausted(c, failoverErr, true)
					return
				}
				if failoverErr.RetryableOnSameAccount {
					retryLimit := account.GetPoolModeRetryCount()
					if sameAccountRetryCount[account.ID] < retryLimit {
						sameAccountRetryCount[account.ID]++
						reqLog.Warn("gemini_images.pool_mode_same_account_retry",
							zap.Int64("account_id", account.ID),
							zap.Int("upstream_status", failoverErr.StatusCode),
							zap.Int("retry_count", sameAccountRetryCount[account.ID]),
						)
						select {
						case <-requestCtx.Done():
							return
						case <-time.After(sameAccountRetryDelay):
						}
						continue
					}
				}
				failedAccountIDs[account.ID] = struct{}{}
				lastFailoverErr = failoverErr
				if switchCount >= maxAccountSwitches {
					h.handleFailoverExhausted(c, failoverErr, false)
					return
				}
				switchCount++
				reqLog.Warn("gemini_images.upstream_failover_switching",
					zap.Int64("account_id", account.ID),
					zap.Int("upstream_status", failoverErr.StatusCode),
					zap.Int("switch_count", switchCount),
					zap.Int("max_switches", maxAccountSwitches),
				)
				continue
			}
			if c.Writer.Size() == writerSizeBeforeForward {
				h.errorResponse(c, http.StatusBadGateway, "upstream_error", "Upstream request failed")
			}
			reqLog.Warn("gemini_images.forward_failed",
				zap.Int64("account_id", account.ID),
				zap.Error(err),
			)
			return
		}

		imageGenRecord.noteSuccess(result)
		recordGeminiImagesUsage(c, h, reqLog, apiKey, subject, subscription, account, result, requestModel, body, parsed, channelMapping)
		reqLog.Debug("gemini_images.request_completed",
			zap.Int64("account_id", account.ID),
			zap.Int("switch_count", switchCount),
		)
		return
	}
}

// buildGeminiImagesAccountSelection 为 gemini 选号结果构造带等待计划的 selection。
// 必须携带 WaitPlan：acquireResponsesAccountSlot 对 Acquired==false 且 WaitPlan==nil
// 的 selection 会直接返回 503（openai_gateway_handler.go:1181-1188）。
// 超时/排队参数取调度配置的 fallback 值（与非粘性调度路径一致），nil cfg 用默认。
func buildGeminiImagesAccountSelection(account *service.Account, cfg *config.Config) *service.AccountSelectionResult {
	waitTimeout := 30 * time.Second
	maxWaiting := 100
	if cfg != nil {
		if cfg.Gateway.Scheduling.FallbackWaitTimeout > 0 {
			waitTimeout = cfg.Gateway.Scheduling.FallbackWaitTimeout
		}
		if cfg.Gateway.Scheduling.FallbackMaxWaiting > 0 {
			maxWaiting = cfg.Gateway.Scheduling.FallbackMaxWaiting
		}
	}
	return &service.AccountSelectionResult{
		Account: account,
		WaitPlan: &service.AccountWaitPlan{
			AccountID:      account.ID,
			MaxConcurrency: account.Concurrency,
			Timeout:        waitTimeout,
			MaxWaiting:     maxWaiting,
		},
	}
}

func recordGeminiImagesUsage(
	c *gin.Context,
	h *OpenAIGatewayHandler,
	reqLog *zap.Logger,
	apiKey *service.APIKey,
	subject middleware2.AuthSubject,
	subscription *service.UserSubscription,
	account *service.Account,
	result *service.OpenAIForwardResult,
	requestModel string,
	body []byte,
	parsed *service.OpenAIImagesRequest,
	channelMapping service.ChannelMappingResult,
) {
	userAgent := c.GetHeader("User-Agent")
	clientIP := ip.GetClientIP(c)
	requestPayloadHash := service.HashUsageRequestPayload(body)
	if parsed.Multipart {
		requestPayloadHash = service.HashUsageRequestPayload([]byte(parsed.StickySessionSeed()))
	}
	inboundEndpoint := GetInboundEndpoint(c)
	upstreamEndpoint := GetUpstreamEndpoint(c, account.Platform)
	quotaPlatform := service.QuotaPlatform(c.Request.Context(), apiKey)
	upstreamModel := ""
	if result != nil {
		upstreamModel = result.UpstreamModel
	}
	h.submitOpenAIUsageRecordTask(c.Request.Context(), result, func(ctx context.Context) {
		if err := h.gatewayService.RecordUsage(ctx, &service.OpenAIRecordUsageInput{
			Result:             result,
			APIKey:             apiKey,
			User:               apiKey.User,
			Account:            account,
			Subscription:       subscription,
			InboundEndpoint:    inboundEndpoint,
			UpstreamEndpoint:   upstreamEndpoint,
			UserAgent:          userAgent,
			IPAddress:          clientIP,
			RequestPayloadHash: requestPayloadHash,
			APIKeyService:      h.apiKeyService,
			QuotaPlatform:      quotaPlatform,
			ChannelUsageFields: channelMapping.ToUsageFields(requestModel, upstreamModel),
		}); err != nil {
			logger.L().With(
				zap.String("component", "handler.openai_gateway.gemini_images"),
				zap.Int64("user_id", subject.UserID),
				zap.Int64("api_key_id", apiKey.ID),
				zap.Any("group_id", apiKey.GroupID),
				zap.String("model", requestModel),
				zap.Int64("account_id", account.ID),
			).Error("gemini_images.record_usage_failed", zap.Error(err))
			reqLog.Debug("gemini_images.record_usage_failed", zap.Error(err))
		}
	})
}
