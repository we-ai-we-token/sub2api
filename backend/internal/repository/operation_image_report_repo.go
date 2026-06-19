package repository

import (
	"context"
	"database/sql"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/service"
)

type operationImageReportRepository struct {
	sql *sql.DB
}

// NewOperationImageReportRepository 构造只读生图报表仓储。
func NewOperationImageReportRepository(db *sql.DB) service.OperationImageReportRepository {
	return &operationImageReportRepository{sql: db}
}

// imageModelWhere 返回识别生图请求的 SQL 片段（不含前导 AND）。
// 依赖 join 别名：usage_logs 为 ul，accounts 为 a。
func imageModelWhere(platform string) string {
	switch platform {
	case service.OperationPlatformGemini:
		return "(a.platform = 'gemini' AND ul.model ILIKE 'gemini-%image%')"
	case service.OperationPlatformOpenAI:
		return "(a.platform = 'openai' AND ul.model ILIKE 'gpt-image-2%')"
	default:
		return "((a.platform = 'openai' AND ul.model ILIKE 'gpt-image-2%') OR (a.platform = 'gemini' AND ul.model ILIKE 'gemini-%image%'))"
	}
}

func bucketIntervalArg(bucket string) string {
	if bucket == "5m" {
		return "5 minutes"
	}
	return "1 hour"
}

func nullableInt64(v *int64) any {
	if v == nil {
		return nil
	}
	return *v
}

func nullFloatPtr(v sql.NullFloat64) *float64 {
	if !v.Valid {
		return nil
	}
	f := v.Float64
	return &f
}

// LatencySeries returns latency percentile time-series for image-generation requests.
func (r *operationImageReportRepository) LatencySeries(_ context.Context, _ service.ImageReportSeriesFilter) ([]service.ImageLatencyBucket, error) {
	return nil, nil
}

// RequestSeries returns success/failure counts time-series for image-generation requests.
func (r *operationImageReportRepository) RequestSeries(_ context.Context, _ service.ImageReportSeriesFilter) ([]service.ImageRequestBucket, error) {
	return nil, nil
}

// TodayBreakdown returns today's image-generation breakdown by platform, model, and group.
func (r *operationImageReportRepository) TodayBreakdown(_ context.Context, _, _ time.Time) ([]service.ImageTodayItem, error) {
	return nil, nil
}

// ListPlatformAccountConcurrency returns accounts with concurrency config for the given platform.
func (r *operationImageReportRepository) ListPlatformAccountConcurrency(_ context.Context, _ string) ([]service.ImageAccountConcurrency, error) {
	return nil, nil
}

// ListAlertAccountConcurrency returns accounts flagged for concurrency alerting.
func (r *operationImageReportRepository) ListAlertAccountConcurrency(_ context.Context) ([]service.ImageAccountConcurrency, error) {
	return nil, nil
}

// DistinctImageModels returns distinct image model names for the given platform.
func (r *operationImageReportRepository) DistinctImageModels(_ context.Context, _ string) ([]string, error) {
	return nil, nil
}

// ListGroups returns all groups for filter option population.
func (r *operationImageReportRepository) ListGroups(_ context.Context) ([]service.ImageReportGroupRef, error) {
	return nil, nil
}
