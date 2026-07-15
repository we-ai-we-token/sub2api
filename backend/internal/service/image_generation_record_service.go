package service

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"
	"unicode/utf8"
)

// 生图记录：每次 Images API 请求（成功 + 失败）落一行 image_generation_records，
// 记录分段耗时（auth/routing/并发槽等待/上游/回传）与重试切号明细，用于瓶颈分析。
// 采集由 handler 层的 imageGenerationRecordState 完成，本文件只定义数据结构与读写服务。

// ImageGenerationAttempt 是一次失败的上游尝试摘要（最终成功的尝试不在其中），
// 由 OpsUpstreamErrorEvent 瘦身而来，存入 attempts_detail JSONB。
type ImageGenerationAttempt struct {
	AtUnixMs           int64  `json:"at_unix_ms,omitempty"`
	AccountID          int64  `json:"account_id,omitempty"`
	AccountName        string `json:"account_name,omitempty"`
	UpstreamStatusCode int    `json:"upstream_status_code,omitempty"`
	UpstreamRequestID  string `json:"upstream_request_id,omitempty"`
	Kind               string `json:"kind,omitempty"`
	Message            string `json:"message,omitempty"`
}

// ImageGenerationRecordInput 是写入 image_generation_records 的一行。
type ImageGenerationRecordInput struct {
	RequestID       string
	ClientRequestID string
	UserID          *int64
	APIKeyID        *int64
	AccountID       *int64
	GroupID         *int64

	Platform      string
	Endpoint      string
	Model         string
	UpstreamModel string
	Stream        bool

	AuthMs            *int64
	RoutingMs         *int64
	ImageSlotWaitMs   *int64
	UserSlotWaitMs    *int64
	AccountSlotWaitMs *int64
	UpstreamMs        *int64
	ResponseMs        *int64
	FirstTokenMs      *int64
	TotalMs           int64

	Attempts           int
	AccountSwitches    int
	SameAccountRetries int
	AttemptsDetail     []*ImageGenerationAttempt

	UpstreamStatusCode   *int
	UpstreamRequestID    string
	UpstreamErrorMessage string

	ImageCount   int
	ImageSize    string
	ImageQuality string

	Success    bool
	StatusCode int
	ErrorType  string

	CreatedAt time.Time
}

// AttemptsDetailJSON 返回 attempts_detail 的 JSON 序列化（空则 nil）。
func (in *ImageGenerationRecordInput) AttemptsDetailJSON() *string {
	if in == nil || len(in.AttemptsDetail) == 0 {
		return nil
	}
	raw, err := json.Marshal(in.AttemptsDetail)
	if err != nil || len(raw) == 0 {
		return nil
	}
	s := string(raw)
	return &s
}

// ImageGenerationRecord 是查询返回的一行（JSON 面向管理端）。
type ImageGenerationRecord struct {
	ID              int64  `json:"id"`
	RequestID       string `json:"request_id,omitempty"`
	ClientRequestID string `json:"client_request_id,omitempty"`
	UserID          *int64 `json:"user_id,omitempty"`
	APIKeyID        *int64 `json:"api_key_id,omitempty"`
	AccountID       *int64 `json:"account_id,omitempty"`
	GroupID         *int64 `json:"group_id,omitempty"`

	Platform      string `json:"platform"`
	Endpoint      string `json:"endpoint"`
	Model         string `json:"model"`
	UpstreamModel string `json:"upstream_model,omitempty"`
	Stream        bool   `json:"stream"`

	AuthMs            *int64 `json:"auth_ms,omitempty"`
	RoutingMs         *int64 `json:"routing_ms,omitempty"`
	ImageSlotWaitMs   *int64 `json:"image_slot_wait_ms,omitempty"`
	UserSlotWaitMs    *int64 `json:"user_slot_wait_ms,omitempty"`
	AccountSlotWaitMs *int64 `json:"account_slot_wait_ms,omitempty"`
	UpstreamMs        *int64 `json:"upstream_ms,omitempty"`
	ResponseMs        *int64 `json:"response_ms,omitempty"`
	FirstTokenMs      *int64 `json:"first_token_ms,omitempty"`
	TotalMs           int64  `json:"total_ms"`

	Attempts           int             `json:"attempts"`
	AccountSwitches    int             `json:"account_switches"`
	SameAccountRetries int             `json:"same_account_retries"`
	AttemptsDetail     json.RawMessage `json:"attempts_detail,omitempty"`

	UpstreamStatusCode   *int   `json:"upstream_status_code,omitempty"`
	UpstreamRequestID    string `json:"upstream_request_id,omitempty"`
	UpstreamErrorMessage string `json:"upstream_error_message,omitempty"`

	ImageCount   int    `json:"image_count"`
	ImageSize    string `json:"image_size,omitempty"`
	ImageQuality string `json:"image_quality,omitempty"`

	Success    bool   `json:"success"`
	StatusCode int    `json:"status_code,omitempty"`
	ErrorType  string `json:"error_type,omitempty"`

	CreatedAt time.Time `json:"created_at"`
}

// ImageGenerationRecordListFilter 是管理端列表查询条件。
type ImageGenerationRecordListFilter struct {
	StartTime time.Time
	EndTime   time.Time

	Platform   string
	Model      string
	Success    *bool
	UserID     *int64
	AccountID  *int64
	GroupID    *int64
	MinTotalMs *int64

	Page     int
	PageSize int
}

type ImageGenerationRecordRepository interface {
	Insert(ctx context.Context, input *ImageGenerationRecordInput) error
	List(ctx context.Context, filter ImageGenerationRecordListFilter) ([]*ImageGenerationRecord, int64, error)
}

type ImageGenerationRecordService struct {
	repo ImageGenerationRecordRepository
}

func NewImageGenerationRecordService(repo ImageGenerationRecordRepository) *ImageGenerationRecordService {
	return &ImageGenerationRecordService{repo: repo}
}

// Record 落一行生图记录。由 usage record worker 池异步调用，失败只由调用方记日志。
func (s *ImageGenerationRecordService) Record(ctx context.Context, input *ImageGenerationRecordInput) error {
	if s == nil || s.repo == nil {
		return fmt.Errorf("image generation record service not configured")
	}
	if input == nil {
		return fmt.Errorf("nil input")
	}
	if input.CreatedAt.IsZero() {
		input.CreatedAt = time.Now()
	}
	input.UpstreamErrorMessage = truncateImageGenText(input.UpstreamErrorMessage, 2048)
	return s.repo.Insert(ctx, input)
}

// List 分页查询生图记录，时间范围缺省为最近 24 小时。
func (s *ImageGenerationRecordService) List(ctx context.Context, filter ImageGenerationRecordListFilter) ([]*ImageGenerationRecord, int64, error) {
	if s == nil || s.repo == nil {
		return nil, 0, fmt.Errorf("image generation record service not configured")
	}
	now := time.Now()
	if filter.EndTime.IsZero() {
		filter.EndTime = now
	}
	if filter.StartTime.IsZero() {
		filter.StartTime = filter.EndTime.Add(-24 * time.Hour)
	}
	if filter.Page < 1 {
		filter.Page = 1
	}
	if filter.PageSize < 1 || filter.PageSize > 1000 {
		filter.PageSize = 20
	}
	filter.Platform = strings.TrimSpace(filter.Platform)
	filter.Model = strings.TrimSpace(filter.Model)
	return s.repo.List(ctx, filter)
}

func truncateImageGenText(s string, max int) string {
	s = strings.TrimSpace(s)
	if max <= 0 || len(s) <= max {
		return s
	}
	cut := s[:max]
	// 不能截断多字节字符，否则 Postgres 会拒绝非法 UTF-8。
	for len(cut) > 0 && !utf8.ValidString(cut) {
		cut = cut[:len(cut)-1]
	}
	return cut
}
