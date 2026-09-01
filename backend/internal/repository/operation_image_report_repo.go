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

// imageModelPredicate 返回识别生图请求的 SQL 片段（不含前导 AND）。
// platformCol / modelCol 为列引用（如 usage_logs 用 a.platform/ul.model，
// ops_error_logs 用 oel.platform/oel.model）。
func imageModelPredicate(platformCol, modelCol, platform string) string {
	openai := fmt.Sprintf("(%s = 'openai' AND %s ILIKE 'gpt-image-2%%')", platformCol, modelCol)
	gemini := fmt.Sprintf("(%s = 'gemini' AND %s ILIKE 'gemini-%%image%%')", platformCol, modelCol)
	switch platform {
	case service.OperationPlatformGemini:
		return gemini
	case service.OperationPlatformOpenAI:
		return openai
	default:
		return "(" + openai + " OR " + gemini + ")"
	}
}

// imageModelWhere 用于 usage_logs（成功侧）：accounts 别名 a、usage_logs 别名 ul。
func imageModelWhere(platform string) string {
	return imageModelPredicate("a.platform", "ul.model", platform)
}

// imageModelWhereErr 用于 ops_error_logs（失败侧）：别名 oel。
func imageModelWhereErr(platform string) string {
	return imageModelPredicate("oel.platform", "oel.model", platform)
}

// modelRestrictionWhere 判断账号「模型限制」（credentials.model_mapping 的 key 即白名单项）
// 是否包含匹配 likePattern 的条目。依赖账号别名 a。likePattern 为字面量（非用户输入）。
func modelRestrictionWhere(likePattern string) string {
	return fmt.Sprintf(`jsonb_typeof(a.credentials -> 'model_mapping') = 'object' AND EXISTS (
		SELECT 1 FROM jsonb_object_keys(a.credentials -> 'model_mapping') k WHERE k ILIKE '%s'
	)`, likePattern)
}

// adobeMembershipWhere 判断 openai api 账号是否属于 adobe 渠道。依赖账号别名 a。
func adobeMembershipWhere() string {
	return `a.id IN (
		SELECT ag.account_id FROM account_groups ag
		JOIN channel_groups cg ON cg.group_id = ag.group_id
		JOIN channels ch ON ch.id = cg.channel_id
		WHERE ch.name = 'adobe' AND ch.status = 'active'
	)`
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

// LatencySeries 返回生图「成功」请求的耗时分位数时间序列（只看 usage_logs）。
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
    AND ($7::bigint IS NULL OR ul.user_id = $7)
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
		bucketIntervalArg(f.Bucket), f.TZ, f.Start, f.End, f.Model, nullableInt64(f.GroupID), nullableInt64(f.UserID))
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

// StageLatencySeries 返回「上游生成 vs 回传客户端」分段耗时分位数曲线。
// 数据源是 image_generation_records（逐请求落库，含分段计时），只取成功请求：
// 失败请求没有完整回传阶段，混进来会把 response 分位数拉低。
//
// 这两条曲线是定责用的：upstream 高说明上游/我们慢；response 高说明客户端
// 下载慢（通常是客户端把自己的带宽切给了过多并发），跟我们无关。
func (r *operationImageReportRepository) StageLatencySeries(ctx context.Context, f service.ImageReportSeriesFilter) ([]service.ImageStageLatencyBucket, error) {
	if r.sql == nil {
		return nil, errors.New("operation image report repository: nil db")
	}
	const query = `
WITH base AS (
  SELECT
    (date_bin($1::interval, r.created_at AT TIME ZONE $2, TIMESTAMP '2000-01-01 00:00:00')) AT TIME ZONE $2 AS bucket_start,
    r.upstream_ms::float8 AS upstream_ms,
    r.response_ms::float8 AS response_ms
  FROM image_generation_records r
  WHERE r.created_at >= $3 AND r.created_at < $4
    AND r.success = TRUE
    AND ($5 = '' OR r.model = $5)
    AND ($6::bigint IS NULL OR r.group_id = $6)
    AND ($7::bigint IS NULL OR r.user_id = $7)
    AND ($8 = '' OR r.platform = $8)
)
SELECT
  bucket_start,
  COUNT(*) AS cnt,
  percentile_cont(0.50) WITHIN GROUP (ORDER BY upstream_ms) AS upstream_p50,
  percentile_cont(0.90) WITHIN GROUP (ORDER BY upstream_ms) AS upstream_p90,
  percentile_cont(0.95) WITHIN GROUP (ORDER BY upstream_ms) AS upstream_p95,
  percentile_cont(0.50) WITHIN GROUP (ORDER BY response_ms) AS response_p50,
  percentile_cont(0.90) WITHIN GROUP (ORDER BY response_ms) AS response_p90,
  percentile_cont(0.95) WITHIN GROUP (ORDER BY response_ms) AS response_p95
FROM base
GROUP BY bucket_start
ORDER BY bucket_start`

	rows, err := r.sql.QueryContext(ctx, query,
		bucketIntervalArg(f.Bucket), f.TZ, f.Start, f.End, f.Model,
		nullableInt64(f.GroupID), nullableInt64(f.UserID), f.Platform)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()

	var out []service.ImageStageLatencyBucket
	for rows.Next() {
		var b service.ImageStageLatencyBucket
		var up50, up90, up95, rp50, rp90, rp95 sql.NullFloat64
		if err := rows.Scan(&b.BucketStart, &b.Count, &up50, &up90, &up95, &rp50, &rp90, &rp95); err != nil {
			return nil, err
		}
		b.UpstreamP50, b.UpstreamP90, b.UpstreamP95 = nullFloatPtr(up50), nullFloatPtr(up90), nullFloatPtr(up95)
		b.ResponseP50, b.ResponseP90, b.ResponseP95 = nullFloatPtr(rp50), nullFloatPtr(rp90), nullFloatPtr(rp95)
		out = append(out, b)
	}
	return out, rows.Err()
}

// RequestSeries 返回生图请求量时间序列：成功来自 usage_logs（actual_cost>0），
// 失败来自 ops_error_logs（status_code>=400）。两源按时间桶 FULL OUTER JOIN 合并。
func (r *operationImageReportRepository) RequestSeries(ctx context.Context, f service.ImageReportSeriesFilter) ([]service.ImageRequestBucket, error) {
	if r.sql == nil {
		return nil, errors.New("operation image report repository: nil db")
	}
	query := fmt.Sprintf(`
WITH succ AS (
  SELECT (date_bin($1::interval, ul.created_at AT TIME ZONE $2, TIMESTAMP '2000-01-01 00:00:00')) AT TIME ZONE $2 AS bucket_start,
         COUNT(*) AS c
  FROM usage_logs ul
  JOIN accounts a ON a.id = ul.account_id
  WHERE ul.created_at >= $3 AND ul.created_at < $4
    AND ul.actual_cost > 0
    AND %s
    AND ($5 = '' OR ul.model = $5)
    AND ($6::bigint IS NULL OR ul.group_id = $6)
    AND ($7::bigint IS NULL OR ul.user_id = $7)
  GROUP BY 1
),
fail AS (
  SELECT (date_bin($1::interval, oel.created_at AT TIME ZONE $2, TIMESTAMP '2000-01-01 00:00:00')) AT TIME ZONE $2 AS bucket_start,
         COUNT(*) AS c
  FROM ops_error_logs oel
  WHERE oel.created_at >= $3 AND oel.created_at < $4
    AND oel.status_code >= 400
    AND oel.is_count_tokens = FALSE
    AND %s
    AND ($5 = '' OR oel.model = $5)
    AND ($6::bigint IS NULL OR oel.group_id = $6)
    AND ($7::bigint IS NULL OR oel.user_id = $7)
  GROUP BY 1
)
SELECT COALESCE(s.bucket_start, fl.bucket_start) AS bucket_start,
       COALESCE(s.c, 0) AS success_count,
       COALESCE(fl.c, 0) AS failure_count
FROM succ s
FULL OUTER JOIN fail fl ON s.bucket_start = fl.bucket_start
ORDER BY bucket_start`, imageModelWhere(f.Platform), imageModelWhereErr(f.Platform))

	rows, err := r.sql.QueryContext(ctx, query,
		bucketIntervalArg(f.Bucket), f.TZ, f.Start, f.End, f.Model, nullableInt64(f.GroupID), nullableInt64(f.UserID))
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

// TodayBreakdown 返回今日生图按平台/模型/分组的成功(usage_logs)/失败(ops_error_logs)拆分。
func (r *operationImageReportRepository) TodayBreakdown(ctx context.Context, start, end time.Time) ([]service.ImageTodayItem, error) {
	if r.sql == nil {
		return nil, errors.New("operation image report repository: nil db")
	}
	query := fmt.Sprintf(`
WITH ev AS (
  SELECT a.platform AS platform, ul.model AS model, ul.group_id AS group_id, 1::bigint AS succ, 0::bigint AS fail
  FROM usage_logs ul
  JOIN accounts a ON a.id = ul.account_id
  WHERE ul.created_at >= $1 AND ul.created_at < $2
    AND ul.actual_cost > 0
    AND %s
  UNION ALL
  SELECT oel.platform, oel.model, oel.group_id, 0::bigint, 1::bigint
  FROM ops_error_logs oel
  WHERE oel.created_at >= $1 AND oel.created_at < $2
    AND oel.status_code >= 400
    AND oel.is_count_tokens = FALSE
    AND %s
),
base AS (
  SELECT ev.platform, ev.model, ev.group_id, g.name AS group_name, ev.succ, ev.fail
  FROM ev
  LEFT JOIN groups g ON g.id = ev.group_id
)
SELECT 'platform' AS dimension, platform AS key, NULL::bigint AS group_id,
       SUM(succ) AS success, SUM(fail) AS failure
FROM base GROUP BY platform
UNION ALL
SELECT 'model', model, NULL::bigint, SUM(succ), SUM(fail)
FROM base GROUP BY model
UNION ALL
SELECT 'group', COALESCE(group_name, 'ungrouped'), group_id, SUM(succ), SUM(fail)
FROM base GROUP BY group_id, group_name
ORDER BY dimension, key`, imageModelWhere(""), imageModelWhereErr(""))

	rows, err := r.sql.QueryContext(ctx, query, start, end)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()

	var out []service.ImageTodayItem
	for rows.Next() {
		var it service.ImageTodayItem
		var gid sql.NullInt64
		if err := rows.Scan(&it.Dimension, &it.Key, &gid, &it.Success, &it.Failure); err != nil {
			return nil, err
		}
		if gid.Valid {
			v := gid.Int64
			it.GroupID = &v
		}
		out = append(out, it)
	}
	return out, rows.Err()
}

// ListImageAccountConcurrency 按生图账号类别返回账号并发上限。
// category: "openai_oauth" / "adobe" / "gemini"。
func (r *operationImageReportRepository) ListImageAccountConcurrency(ctx context.Context, category string) ([]service.ImageAccountConcurrency, error) {
	if r.sql == nil {
		return nil, errors.New("operation image report repository: nil db")
	}
	var cond string
	switch category {
	case service.ImageAccountCategoryOpenAIOAuth:
		cond = "a.platform = 'openai' AND a.type = 'oauth' AND " + modelRestrictionWhere("gpt-image-2%")
	case service.ImageAccountCategoryAdobe:
		cond = "a.platform = 'openai' AND a.type <> 'oauth' AND " + adobeMembershipWhere() + " AND " + modelRestrictionWhere("gpt-image-2%")
	case service.ImageAccountCategoryGemini:
		cond = "a.platform = 'gemini' AND " + modelRestrictionWhere("gemini-%image%")
	default:
		return nil, fmt.Errorf("operation image report repository: unknown account category %q", category)
	}
	query := "SELECT a.id, a.concurrency FROM accounts a WHERE a.deleted_at IS NULL AND a.status = 'active' AND " + cond
	rows, err := r.sql.QueryContext(ctx, query)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	return scanAccountConcurrency(rows)
}

// ListAlertAccountConcurrency 返回告警账号：openai oauth 生图账号（模型限制含 gpt-image-2*）
// 且 5h 或 7d 用量 >= 90%。
func (r *operationImageReportRepository) ListAlertAccountConcurrency(ctx context.Context) ([]service.ImageAccountConcurrency, error) {
	if r.sql == nil {
		return nil, errors.New("operation image report repository: nil db")
	}
	query := `
SELECT a.id, a.concurrency FROM accounts a
WHERE a.deleted_at IS NULL AND a.status = 'active' AND a.platform = 'openai' AND a.type = 'oauth'
  AND ` + modelRestrictionWhere("gpt-image-2%") + `
  AND (
    ((a.extra->>'codex_5h_used_percent') ~ '^[0-9]+(\.[0-9]+)?$' AND (a.extra->>'codex_5h_used_percent')::float8 >= 90)
    OR ((a.extra->>'codex_7d_used_percent') ~ '^[0-9]+(\.[0-9]+)?$' AND (a.extra->>'codex_7d_used_percent')::float8 >= 90)
  )`
	rows, err := r.sql.QueryContext(ctx, query)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	return scanAccountConcurrency(rows)
}

func scanAccountConcurrency(rows *sql.Rows) ([]service.ImageAccountConcurrency, error) {
	var out []service.ImageAccountConcurrency
	for rows.Next() {
		var a service.ImageAccountConcurrency
		if err := rows.Scan(&a.AccountID, &a.Concurrency); err != nil {
			return nil, err
		}
		out = append(out, a)
	}
	return out, rows.Err()
}

// DistinctImageModels 返回近 30 天出现过的生图模型名（成功侧 usage_logs）。
func (r *operationImageReportRepository) DistinctImageModels(ctx context.Context, platform string) ([]string, error) {
	if r.sql == nil {
		return nil, errors.New("operation image report repository: nil db")
	}
	query := fmt.Sprintf(`
SELECT DISTINCT ul.model
FROM usage_logs ul
JOIN accounts a ON a.id = ul.account_id
WHERE ul.created_at >= NOW() - INTERVAL '30 days'
  AND ul.model <> ''
  AND %s
ORDER BY ul.model`, imageModelWhere(platform))
	rows, err := r.sql.QueryContext(ctx, query)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	var out []string
	for rows.Next() {
		var m string
		if err := rows.Scan(&m); err != nil {
			return nil, err
		}
		out = append(out, m)
	}
	return out, rows.Err()
}

// ListGroups 返回全部分组，供筛选下拉使用。
func (r *operationImageReportRepository) ListGroups(ctx context.Context) ([]service.ImageReportGroupRef, error) {
	if r.sql == nil {
		return nil, errors.New("operation image report repository: nil db")
	}
	rows, err := r.sql.QueryContext(ctx, `
SELECT id, name FROM groups WHERE deleted_at IS NULL ORDER BY name`)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	var out []service.ImageReportGroupRef
	for rows.Next() {
		var g service.ImageReportGroupRef
		if err := rows.Scan(&g.ID, &g.Name); err != nil {
			return nil, err
		}
		out = append(out, g)
	}
	return out, rows.Err()
}
