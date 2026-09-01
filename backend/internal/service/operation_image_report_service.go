package service

import (
	"context"
	"fmt"
	"strings"
	"time"
)

const (
	OperationPlatformOpenAI = "openai"
	OperationPlatformGemini = "gemini"

	operationBucket5m = "5m"
	operationBucket1h = "1h"

	// 生图并发卡片的账号类别。
	ImageAccountCategoryOpenAIOAuth = "openai_oauth" // OpenAI OAuth(codex) 生图账号
	ImageAccountCategoryAdobe       = "adobe"        // Adobe 渠道(OpenAI API) 生图账号
	ImageAccountCategoryGemini      = "gemini"       // Gemini 生图账号
)

// ImageReportSeriesFilter 是耗时/请求量曲线的公共筛选。
type ImageReportSeriesFilter struct {
	Platform string // "openai" | "gemini"
	Model    string // 可选精确模型；"" 表示全部
	GroupID  *int64 // 可选
	UserID   *int64 // 可选：按用户筛选
	Bucket   string // "5m" | "1h"
	TZ       string // IANA 时区
	Start    time.Time
	End      time.Time
}

type ImageLatencyBucket struct {
	BucketStart time.Time `json:"bucket_start"`
	Count       int64     `json:"count"`
	MinMs       *float64  `json:"min_ms"`
	P25Ms       *float64  `json:"p25_ms"`
	P50Ms       *float64  `json:"p50_ms"`
	P75Ms       *float64  `json:"p75_ms"`
	MaxMs       *float64  `json:"max_ms"`
	AvgMs       *float64  `json:"avg_ms"`
}

// ImageStageLatencyBucket 是「上游生成 vs 回传客户端」分段耗时的分位数桶。
// 数据源为 image_generation_records（逐请求落库），只统计成功请求：
// 失败请求没有完整的回传阶段，混进来会把分位数拉偏。
type ImageStageLatencyBucket struct {
	BucketStart time.Time `json:"bucket_start"`
	Count       int64     `json:"count"`
	UpstreamP50 *float64  `json:"upstream_p50_ms"`
	UpstreamP90 *float64  `json:"upstream_p90_ms"`
	UpstreamP95 *float64  `json:"upstream_p95_ms"`
	ResponseP50 *float64  `json:"response_p50_ms"`
	ResponseP90 *float64  `json:"response_p90_ms"`
	ResponseP95 *float64  `json:"response_p95_ms"`
}

type ImageRequestBucket struct {
	BucketStart  time.Time `json:"bucket_start"`
	SuccessCount int64     `json:"success_count"`
	FailureCount int64     `json:"failure_count"`
	SuccessRate  float64   `json:"success_rate"`
}

type ImageTodayItem struct {
	Dimension string `json:"dimension"` // "platform" | "model" | "group"
	Key       string `json:"key"`
	GroupID   *int64 `json:"group_id,omitempty"`
	Success   int64  `json:"success"`
	Failure   int64  `json:"failure"`
}

type ImageAccountConcurrency struct {
	AccountID   int64
	Concurrency int // 配置上限
}

type ImageReportGroupRef struct {
	ID   int64  `json:"id"`
	Name string `json:"name"`
}

type ImageConcurrencyCard struct {
	Key                string `json:"key"` // "openai_oauth" | "adobe" | "gemini"
	CurrentConcurrency int    `json:"current_concurrency"`
	TotalConcurrency   int    `json:"total_concurrency"`
	Available          bool   `json:"available"`
}

type ImageAlertConcurrency struct {
	AccountCount       int  `json:"account_count"`
	CurrentConcurrency int  `json:"current_concurrency"`
	TotalConcurrency   int  `json:"total_concurrency"`
	Available          bool `json:"available"`
}

type ImageConcurrencyOverview struct {
	Cards []ImageConcurrencyCard `json:"cards"`
	Alert ImageAlertConcurrency  `json:"alert"`
}

type ImageReportFilterOptions struct {
	Models []string              `json:"models"`
	Groups []ImageReportGroupRef `json:"groups"`
}

// OperationImageReportRepository 由 package repository 实现（仅只读）。
type OperationImageReportRepository interface {
	LatencySeries(ctx context.Context, f ImageReportSeriesFilter) ([]ImageLatencyBucket, error)
	StageLatencySeries(ctx context.Context, f ImageReportSeriesFilter) ([]ImageStageLatencyBucket, error)
	RequestSeries(ctx context.Context, f ImageReportSeriesFilter) ([]ImageRequestBucket, error)
	TodayBreakdown(ctx context.Context, start, end time.Time) ([]ImageTodayItem, error)
	ListImageAccountConcurrency(ctx context.Context, category string) ([]ImageAccountConcurrency, error)
	ListAlertAccountConcurrency(ctx context.Context) ([]ImageAccountConcurrency, error)
	DistinctImageModels(ctx context.Context, platform string) ([]string, error)
	ListGroups(ctx context.Context) ([]ImageReportGroupRef, error)
}

// accountConcurrencyReader 是 *ConcurrencyService 的只读子集，便于测试替身。
type accountConcurrencyReader interface {
	GetAccountConcurrencyBatch(ctx context.Context, accountIDs []int64) (map[int64]int, error)
}

// OperationImageReportService 组装仓储与并发读取。
type OperationImageReportService struct {
	repo        OperationImageReportRepository
	concurrency accountConcurrencyReader
}

func NewOperationImageReportService(repo OperationImageReportRepository, concurrency *ConcurrencyService) *OperationImageReportService {
	return &OperationImageReportService{repo: repo, concurrency: concurrency}
}

func normalizePlatform(p string) string {
	if strings.EqualFold(strings.TrimSpace(p), OperationPlatformGemini) {
		return OperationPlatformGemini
	}
	return OperationPlatformOpenAI
}

func normalizeBucket(bucket string) string {
	if bucket == operationBucket5m {
		return operationBucket5m
	}
	return operationBucket1h
}

func normalizeTZ(tz string) string {
	tz = strings.TrimSpace(tz)
	if tz == "" {
		return "UTC"
	}
	if _, err := time.LoadLocation(tz); err != nil {
		return "UTC"
	}
	return tz
}

func todayWindow(tz string, now time.Time) (time.Time, time.Time) {
	loc, err := time.LoadLocation(tz)
	if err != nil {
		loc = time.UTC
	}
	ln := now.In(loc)
	start := time.Date(ln.Year(), ln.Month(), ln.Day(), 0, 0, 0, 0, loc)
	return start, now
}

// BuildSeriesFilter 归一化原始查询参数并固定近 24h 窗口。
func (s *OperationImageReportService) BuildSeriesFilter(platform, model string, groupID, userID *int64, bucket, tz string, now time.Time) ImageReportSeriesFilter {
	return ImageReportSeriesFilter{
		Platform: normalizePlatform(platform),
		Model:    strings.TrimSpace(model),
		GroupID:  groupID,
		UserID:   userID,
		Bucket:   normalizeBucket(bucket),
		TZ:       normalizeTZ(tz),
		Start:    now.Add(-24 * time.Hour),
		End:      now,
	}
}

func (s *OperationImageReportService) LatencySeries(ctx context.Context, f ImageReportSeriesFilter) ([]ImageLatencyBucket, error) {
	return s.repo.LatencySeries(ctx, f)
}

// StageLatencySeries 返回「上游生成 vs 回传客户端」的分段耗时分位数曲线。
// upstream 高 = 上游/我们慢；response 高 = 客户端下载慢（带宽被自身并发瓜分）。
func (s *OperationImageReportService) StageLatencySeries(ctx context.Context, f ImageReportSeriesFilter) ([]ImageStageLatencyBucket, error) {
	return s.repo.StageLatencySeries(ctx, f)
}

func (s *OperationImageReportService) RequestSeries(ctx context.Context, f ImageReportSeriesFilter) ([]ImageRequestBucket, error) {
	buckets, err := s.repo.RequestSeries(ctx, f)
	if err != nil {
		return nil, err
	}
	for i := range buckets {
		total := buckets[i].SuccessCount + buckets[i].FailureCount
		if total > 0 {
			buckets[i].SuccessRate = float64(buckets[i].SuccessCount) / float64(total)
		}
	}
	return buckets, nil
}

func (s *OperationImageReportService) TodayBreakdown(ctx context.Context, tz string, now time.Time) ([]ImageTodayItem, error) {
	start, end := todayWindow(normalizeTZ(tz), now)
	return s.repo.TodayBreakdown(ctx, start, end)
}

func (s *OperationImageReportService) FilterOptions(ctx context.Context, platform string) (*ImageReportFilterOptions, error) {
	models, err := s.repo.DistinctImageModels(ctx, normalizePlatform(platform))
	if err != nil {
		return nil, err
	}
	groups, err := s.repo.ListGroups(ctx)
	if err != nil {
		return nil, err
	}
	return &ImageReportFilterOptions{Models: models, Groups: groups}, nil
}

func (s *OperationImageReportService) Concurrency(ctx context.Context) (*ImageConcurrencyOverview, error) {
	categories := []string{
		ImageAccountCategoryOpenAIOAuth,
		ImageAccountCategoryAdobe,
		ImageAccountCategoryGemini,
	}
	overview := &ImageConcurrencyOverview{Cards: make([]ImageConcurrencyCard, 0, len(categories))}
	for _, cat := range categories {
		accounts, err := s.repo.ListImageAccountConcurrency(ctx, cat)
		if err != nil {
			return nil, fmt.Errorf("list %s account concurrency: %w", cat, err)
		}
		card := ImageConcurrencyCard{Key: cat}
		ids := make([]int64, 0, len(accounts))
		for _, a := range accounts {
			card.TotalConcurrency += a.Concurrency
			ids = append(ids, a.AccountID)
		}
		card.CurrentConcurrency, card.Available = s.sumCurrent(ctx, ids)
		overview.Cards = append(overview.Cards, card)
	}
	alertAccounts, err := s.repo.ListAlertAccountConcurrency(ctx)
	if err != nil {
		return nil, fmt.Errorf("list alert account concurrency: %w", err)
	}
	alert := ImageAlertConcurrency{AccountCount: len(alertAccounts)}
	ids := make([]int64, 0, len(alertAccounts))
	for _, a := range alertAccounts {
		alert.TotalConcurrency += a.Concurrency
		ids = append(ids, a.AccountID)
	}
	alert.CurrentConcurrency, alert.Available = s.sumCurrent(ctx, ids)
	overview.Alert = alert
	return overview, nil
}

// newOperationImageReportServiceForTest 用测试替身注入并发读取器（生产用 NewOperationImageReportService）。
func newOperationImageReportServiceForTest(repo OperationImageReportRepository, reader accountConcurrencyReader) *OperationImageReportService {
	return &OperationImageReportService{repo: repo, concurrency: reader}
}

// sumCurrent 求一批账号当前并发之和；Redis 不可用时返回 (0,false) 表示降级。
func (s *OperationImageReportService) sumCurrent(ctx context.Context, ids []int64) (int, bool) {
	if len(ids) == 0 {
		return 0, true
	}
	if s.concurrency == nil {
		return 0, false
	}
	m, err := s.concurrency.GetAccountConcurrencyBatch(ctx, ids)
	if err != nil {
		return 0, false
	}
	total := 0
	for _, v := range m {
		total += v
	}
	return total, true
}
