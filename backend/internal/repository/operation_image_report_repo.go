package repository

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
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
func (r *operationImageReportRepository) LatencySeries(ctx context.Context, f service.ImageReportSeriesFilter) ([]service.ImageLatencyBucket, error) {
	if r.sql == nil {
		return nil, errors.New("operation image report repository: nil db")
	}
	query := fmt.Sprintf(`
WITH base AS (
  SELECT
    (date_bin($1::interval, ul.created_at AT TIME ZONE $2, TIMESTAMP '2000-01-01 00:00:00')) AT TIME ZONE $2 AS bucket_start,
    ul.duration_ms AS duration_ms
  FROM usage_logs ul
  JOIN accounts a ON a.id = ul.account_id
  WHERE ul.created_at >= $3 AND ul.created_at < $4
    AND ul.actual_cost > 0
    AND ul.duration_ms IS NOT NULL
    AND %s
    AND ($5 = '' OR ul.model = $5)
    AND ($6::bigint IS NULL OR ul.group_id = $6)
)
SELECT
  bucket_start,
  COUNT(*) AS cnt,
  MIN(duration_ms)::float8 AS min_ms,
  percentile_cont(0.25) WITHIN GROUP (ORDER BY duration_ms) AS p25_ms,
  percentile_cont(0.50) WITHIN GROUP (ORDER BY duration_ms) AS p50_ms,
  percentile_cont(0.75) WITHIN GROUP (ORDER BY duration_ms) AS p75_ms,
  MAX(duration_ms)::float8 AS max_ms,
  AVG(duration_ms)::float8 AS avg_ms
FROM base
GROUP BY bucket_start
ORDER BY bucket_start`, imageModelWhere(f.Platform))

	rows, err := r.sql.QueryContext(ctx, query,
		bucketIntervalArg(f.Bucket), f.TZ, f.Start, f.End, f.Model, nullableInt64(f.GroupID))
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()

	var out []service.ImageLatencyBucket
	for rows.Next() {
		var b service.ImageLatencyBucket
		var minMs, p25, p50, p75, maxMs, avgMs sql.NullFloat64
		if err := rows.Scan(&b.BucketStart, &b.Count, &minMs, &p25, &p50, &p75, &maxMs, &avgMs); err != nil {
			return nil, err
		}
		b.MinMs, b.P25Ms, b.P50Ms = nullFloatPtr(minMs), nullFloatPtr(p25), nullFloatPtr(p50)
		b.P75Ms, b.MaxMs, b.AvgMs = nullFloatPtr(p75), nullFloatPtr(maxMs), nullFloatPtr(avgMs)
		out = append(out, b)
	}
	return out, rows.Err()
}

// RequestSeries returns success/failure counts time-series for image-generation requests.
func (r *operationImageReportRepository) RequestSeries(ctx context.Context, f service.ImageReportSeriesFilter) ([]service.ImageRequestBucket, error) {
	if r.sql == nil {
		return nil, errors.New("operation image report repository: nil db")
	}
	query := fmt.Sprintf(`
WITH base AS (
  SELECT
    (date_bin($1::interval, ul.created_at AT TIME ZONE $2, TIMESTAMP '2000-01-01 00:00:00')) AT TIME ZONE $2 AS bucket_start,
    (ul.actual_cost > 0) AS success
  FROM usage_logs ul
  JOIN accounts a ON a.id = ul.account_id
  WHERE ul.created_at >= $3 AND ul.created_at < $4
    AND %s
    AND ($5 = '' OR ul.model = $5)
    AND ($6::bigint IS NULL OR ul.group_id = $6)
)
SELECT bucket_start,
  COUNT(*) FILTER (WHERE success) AS success_count,
  COUNT(*) FILTER (WHERE NOT success) AS failure_count
FROM base
GROUP BY bucket_start
ORDER BY bucket_start`, imageModelWhere(f.Platform))

	rows, err := r.sql.QueryContext(ctx, query,
		bucketIntervalArg(f.Bucket), f.TZ, f.Start, f.End, f.Model, nullableInt64(f.GroupID))
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()

	var out []service.ImageRequestBucket
	for rows.Next() {
		var b service.ImageRequestBucket
		if err := rows.Scan(&b.BucketStart, &b.SuccessCount, &b.FailureCount); err != nil {
			return nil, err
		}
		out = append(out, b)
	}
	return out, rows.Err()
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
