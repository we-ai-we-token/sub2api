# 运营管理 / 生图报表 Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** 在 admin 后台新增一级菜单「运营管理」及其首个二级页面「生图报表」，对 OpenAI/Gemini 生图调用做只读的运营监控（并发卡片、今日看板、耗时曲线、请求量曲线）。

**Architecture:** 纯只读、与现有业务逻辑解耦。后端沿用既有分层（repository 原生 SQL / service 组装 / handler HTTP / routes），用 `operation_image_report_*` 前缀文件自成一片；仅读 `usage_logs`/`accounts`/`groups` 与 Redis 并发计数，不写库、不改任何现有写入路径。前端新增独立页面 + chart.js 图表，挂到侧边栏新一级菜单。

**Tech Stack:** Go + Gin + Ent(PostgreSQL 15) + google wire + Redis；Vue3 + TS + Pinia + chart.js/vue-chartjs + vue-i18n；测试用 go-sqlmock（单元）+ testcontainers（集成，`-tags=integration`）+ vitest。

## Global Constraints

- 生图识别按**模型名**：openai `ul.model ILIKE 'gpt-image-2%'`；gemini `ul.model ILIKE 'gemini-%image%'`。绝不用 `image_count` 识别（漏失败请求）。
- 成功/失败：`actual_cost > 0` 为成功，`= 0` 为失败。不新增列、不改写入逻辑。
- 所有 DB 访问只读（SELECT）。所有查询带 `created_at` 时间范围谓词。
- 时区：以请求参数 `tz`（IANA）为准切「今天」和时间桶；非法/空回退 `UTC`。
- 曲线窗口最多近 24h；`bucket` 仅 `5m`/`1h`，非法回退 `1h`。
- 模块入口：`/api/v1/admin/operation/image-report/*`，复用现有 admin 鉴权中间件。
- 包导入无环：新 service 在 `package service`（定义 `OperationImageReportRepository` 接口），新 repo 在 `package repository`（实现接口，仅持 `*sql.DB`）。repository 已依赖 service，不可反向。
- 提交规范：conventional commits，类型 `feat`/`test`/`chore`。
- 工作分支：`feature/operation-image-report`（已从 `release` 切出）。

---

## Phase 1 — 后端数据层（package repository）

### Task 1: 领域类型 + 仓储接口 + 生图模型谓词

**Files:**
- Create: `backend/internal/service/operation_image_report_service.go`
- Create: `backend/internal/repository/operation_image_report_repo.go`
- Test: `backend/internal/repository/operation_image_report_repo_test.go`

**Interfaces:**
- Produces (service 包): 见下方类型与 `OperationImageReportRepository` 接口；常量 `OperationPlatformOpenAI="openai"`、`OperationPlatformGemini="gemini"`。
- Produces (repository 包): `imageModelWhere(platform string) string`、`bucketIntervalArg(bucket string) string`、`NewOperationImageReportRepository(db *sql.DB) service.OperationImageReportRepository`。

- [ ] **Step 1: 写 service 包类型与接口文件**

Create `backend/internal/service/operation_image_report_service.go`:

```go
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
)

// ImageReportSeriesFilter 是耗时/请求量曲线的公共筛选。
type ImageReportSeriesFilter struct {
	Platform string // "openai" | "gemini"
	Model    string // 可选精确模型；"" 表示全部
	GroupID  *int64 // 可选
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
	Platform           string `json:"platform"`
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
	RequestSeries(ctx context.Context, f ImageReportSeriesFilter) ([]ImageRequestBucket, error)
	TodayBreakdown(ctx context.Context, start, end time.Time) ([]ImageTodayItem, error)
	ListPlatformAccountConcurrency(ctx context.Context, platform string) ([]ImageAccountConcurrency, error)
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
func (s *OperationImageReportService) BuildSeriesFilter(platform, model string, groupID *int64, bucket, tz string, now time.Time) ImageReportSeriesFilter {
	return ImageReportSeriesFilter{
		Platform: normalizePlatform(platform),
		Model:    strings.TrimSpace(model),
		GroupID:  groupID,
		Bucket:   normalizeBucket(bucket),
		TZ:       normalizeTZ(tz),
		Start:    now.Add(-24 * time.Hour),
		End:      now,
	}
}

func (s *OperationImageReportService) LatencySeries(ctx context.Context, f ImageReportSeriesFilter) ([]ImageLatencyBucket, error) {
	return s.repo.LatencySeries(ctx, f)
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
	platforms := []string{OperationPlatformOpenAI, OperationPlatformGemini}
	overview := &ImageConcurrencyOverview{Cards: make([]ImageConcurrencyCard, 0, len(platforms))}
	for _, p := range platforms {
		accounts, err := s.repo.ListPlatformAccountConcurrency(ctx, p)
		if err != nil {
			return nil, fmt.Errorf("list %s account concurrency: %w", p, err)
		}
		card := ImageConcurrencyCard{Platform: p}
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
```

- [ ] **Step 2: 写 repository 谓词 + 构造函数的失败测试**

Create `backend/internal/repository/operation_image_report_repo_test.go`:

```go
package repository

import (
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/stretchr/testify/require"
)

func TestImageModelWhere(t *testing.T) {
	require.Contains(t, imageModelWhere(service.OperationPlatformOpenAI), "gpt-image-2%")
	require.NotContains(t, imageModelWhere(service.OperationPlatformOpenAI), "gemini")
	require.Contains(t, imageModelWhere(service.OperationPlatformGemini), "gemini-%image%")
	require.NotContains(t, imageModelWhere(service.OperationPlatformGemini), "gpt-image")
	both := imageModelWhere("")
	require.Contains(t, both, "gpt-image-2%")
	require.Contains(t, both, "gemini-%image%")
}

func TestBucketIntervalArg(t *testing.T) {
	require.Equal(t, "5 minutes", bucketIntervalArg("5m"))
	require.Equal(t, "1 hour", bucketIntervalArg("1h"))
	require.Equal(t, "1 hour", bucketIntervalArg("garbage"))
}
```

- [ ] **Step 3: 运行测试确认失败（未定义）**

Run: `cd backend && go test ./internal/repository/ -run 'TestImageModelWhere|TestBucketIntervalArg'`
Expected: FAIL（编译错误：`imageModelWhere`/`bucketIntervalArg` undefined）

- [ ] **Step 4: 写 repo 骨架与谓词**

Create `backend/internal/repository/operation_image_report_repo.go`:

```go
package repository

import (
	"database/sql"

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
```

- [ ] **Step 5: 运行测试确认通过**

Run: `cd backend && go test ./internal/repository/ -run 'TestImageModelWhere|TestBucketIntervalArg'`
Expected: PASS

- [ ] **Step 6: 提交**

```bash
git add backend/internal/service/operation_image_report_service.go backend/internal/repository/operation_image_report_repo.go backend/internal/repository/operation_image_report_repo_test.go
git commit -m "feat(operation): add image-report domain types, repo interface and predicates"
```

注：此时 service 文件引用了未实现的 repo 方法，但 repo 通过接口在运行时绑定；service 包能独立编译（接口已定义）。若 `go build ./internal/service/` 报错，确认接口方法签名与 service 调用一致。

---

### Task 2: RequestSeries 查询（请求量：成功/失败）

**Files:**
- Modify: `backend/internal/repository/operation_image_report_repo.go`
- Test: `backend/internal/repository/operation_image_report_repo_test.go`

**Interfaces:**
- Consumes: `service.ImageReportSeriesFilter`、`imageModelWhere`、`bucketIntervalArg`、`nullableInt64`。
- Produces: `(*operationImageReportRepository) RequestSeries(ctx, f) ([]service.ImageRequestBucket, error)`。

- [ ] **Step 1: 写 sqlmock 失败测试**

Append to `operation_image_report_repo_test.go`:

```go
import (
	"context"
	"time"

	"github.com/DATA-DOG/go-sqlmock"
)

func TestOperationImageReportRequestSeries(t *testing.T) {
	db, mock := newSQLMock(t)
	repo := &operationImageReportRepository{sql: db}

	start := time.Date(2025, 1, 1, 0, 0, 0, 0, time.UTC)
	end := start.Add(24 * time.Hour)
	b0 := time.Date(2025, 1, 1, 1, 0, 0, 0, time.UTC)

	mock.ExpectQuery("FROM usage_logs ul").
		WithArgs("1 hour", "UTC", start, end, "", nil).
		WillReturnRows(sqlmock.NewRows([]string{"bucket_start", "success_count", "failure_count"}).
			AddRow(b0, int64(8), int64(2)))

	got, err := repo.RequestSeries(context.Background(), service.ImageReportSeriesFilter{
		Platform: service.OperationPlatformOpenAI, Bucket: "1h", TZ: "UTC", Start: start, End: end,
	})
	require.NoError(t, err)
	require.Len(t, got, 1)
	require.Equal(t, int64(8), got[0].SuccessCount)
	require.Equal(t, int64(2), got[0].FailureCount)
	require.NoError(t, mock.ExpectationsWereMet())
}
```

- [ ] **Step 2: 运行确认失败**

Run: `cd backend && go test ./internal/repository/ -run TestOperationImageReportRequestSeries`
Expected: FAIL（`RequestSeries` undefined）

- [ ] **Step 3: 实现 RequestSeries**

Append to `operation_image_report_repo.go`:

```go
import (
	"context"
	"errors"
	"fmt"
)

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
```

- [ ] **Step 4: 运行确认通过**

Run: `cd backend && go test ./internal/repository/ -run TestOperationImageReportRequestSeries`
Expected: PASS

- [ ] **Step 5: 提交**

```bash
git add backend/internal/repository/operation_image_report_repo.go backend/internal/repository/operation_image_report_repo_test.go
git commit -m "feat(operation): add image request-volume series query"
```

---

### Task 3: LatencySeries 查询（成功耗时分位数）

**Files:**
- Modify: `backend/internal/repository/operation_image_report_repo.go`
- Test: `backend/internal/repository/operation_image_report_repo_test.go`

**Interfaces:**
- Produces: `(*operationImageReportRepository) LatencySeries(ctx, f) ([]service.ImageLatencyBucket, error)`。

- [ ] **Step 1: 写 sqlmock 失败测试**

Append:

```go
import "database/sql"

func TestOperationImageReportLatencySeries(t *testing.T) {
	db, mock := newSQLMock(t)
	repo := &operationImageReportRepository{sql: db}

	start := time.Date(2025, 1, 1, 0, 0, 0, 0, time.UTC)
	end := start.Add(24 * time.Hour)
	b0 := time.Date(2025, 1, 1, 1, 0, 0, 0, time.UTC)
	gid := int64(7)

	mock.ExpectQuery("percentile_cont").
		WithArgs("5 minutes", "Asia/Shanghai", start, end, "gpt-image-2", gid).
		WillReturnRows(sqlmock.NewRows([]string{"bucket_start", "cnt", "min_ms", "p25_ms", "p50_ms", "p75_ms", "max_ms", "avg_ms"}).
			AddRow(b0, int64(4), 100.0, 120.0, 150.0, 200.0, 400.0, 180.0))

	got, err := repo.LatencySeries(context.Background(), service.ImageReportSeriesFilter{
		Platform: service.OperationPlatformOpenAI, Model: "gpt-image-2", GroupID: &gid,
		Bucket: "5m", TZ: "Asia/Shanghai", Start: start, End: end,
	})
	require.NoError(t, err)
	require.Len(t, got, 1)
	require.Equal(t, int64(4), got[0].Count)
	require.NotNil(t, got[0].P50Ms)
	require.Equal(t, 150.0, *got[0].P50Ms)
	require.NoError(t, mock.ExpectationsWereMet())
}
```

- [ ] **Step 2: 运行确认失败**

Run: `cd backend && go test ./internal/repository/ -run TestOperationImageReportLatencySeries`
Expected: FAIL（`LatencySeries` undefined）

- [ ] **Step 3: 实现 LatencySeries**

Append to `operation_image_report_repo.go`:

```go
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
```

- [ ] **Step 4: 运行确认通过**

Run: `cd backend && go test ./internal/repository/ -run TestOperationImageReportLatencySeries`
Expected: PASS

- [ ] **Step 5: 提交**

```bash
git add backend/internal/repository/operation_image_report_repo.go backend/internal/repository/operation_image_report_repo_test.go
git commit -m "feat(operation): add image success-latency percentile series query"
```

---

### Task 4: TodayBreakdown + 账号并发列表 + filters 查询

**Files:**
- Modify: `backend/internal/repository/operation_image_report_repo.go`
- Test: `backend/internal/repository/operation_image_report_repo_test.go`

**Interfaces:**
- Produces: `TodayBreakdown`、`ListPlatformAccountConcurrency`、`ListAlertAccountConcurrency`、`DistinctImageModels`、`ListGroups`（实现 `service.OperationImageReportRepository` 剩余方法）。

- [ ] **Step 1: 写 sqlmock 失败测试（覆盖 5 个方法）**

Append:

```go
func TestOperationImageReportTodayBreakdown(t *testing.T) {
	db, mock := newSQLMock(t)
	repo := &operationImageReportRepository{sql: db}
	start := time.Date(2025, 1, 2, 0, 0, 0, 0, time.UTC)
	end := start.Add(12 * time.Hour)

	mock.ExpectQuery("UNION ALL").
		WithArgs(start, end).
		WillReturnRows(sqlmock.NewRows([]string{"dimension", "key", "group_id", "success", "failure"}).
			AddRow("platform", "openai", nil, int64(10), int64(1)).
			AddRow("model", "gpt-image-2", nil, int64(10), int64(1)).
			AddRow("group", "team-a", int64(3), int64(10), int64(1)))

	got, err := repo.TodayBreakdown(context.Background(), start, end)
	require.NoError(t, err)
	require.Len(t, got, 3)
	require.Equal(t, "platform", got[0].Dimension)
	require.Nil(t, got[0].GroupID)
	require.NotNil(t, got[2].GroupID)
	require.Equal(t, int64(3), *got[2].GroupID)
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestOperationImageReportListPlatformAccountConcurrency(t *testing.T) {
	db, mock := newSQLMock(t)
	repo := &operationImageReportRepository{sql: db}
	mock.ExpectQuery("FROM accounts").
		WithArgs("openai").
		WillReturnRows(sqlmock.NewRows([]string{"id", "concurrency"}).
			AddRow(int64(1), 3).AddRow(int64(2), 5))
	got, err := repo.ListPlatformAccountConcurrency(context.Background(), "openai")
	require.NoError(t, err)
	require.Len(t, got, 2)
	require.Equal(t, 5, got[1].Concurrency)
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestOperationImageReportListAlertAccountConcurrency(t *testing.T) {
	db, mock := newSQLMock(t)
	repo := &operationImageReportRepository{sql: db}
	mock.ExpectQuery("codex_5h_used_percent").
		WillReturnRows(sqlmock.NewRows([]string{"id", "concurrency"}).AddRow(int64(9), 4))
	got, err := repo.ListAlertAccountConcurrency(context.Background())
	require.NoError(t, err)
	require.Len(t, got, 1)
	require.Equal(t, int64(9), got[0].AccountID)
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestOperationImageReportDistinctImageModels(t *testing.T) {
	db, mock := newSQLMock(t)
	repo := &operationImageReportRepository{sql: db}
	mock.ExpectQuery("SELECT DISTINCT ul.model").
		WillReturnRows(sqlmock.NewRows([]string{"model"}).AddRow("gpt-image-2").AddRow("gpt-image-2-mini"))
	got, err := repo.DistinctImageModels(context.Background(), "openai")
	require.NoError(t, err)
	require.Equal(t, []string{"gpt-image-2", "gpt-image-2-mini"}, got)
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestOperationImageReportListGroups(t *testing.T) {
	db, mock := newSQLMock(t)
	repo := &operationImageReportRepository{sql: db}
	mock.ExpectQuery("FROM groups").
		WillReturnRows(sqlmock.NewRows([]string{"id", "name"}).AddRow(int64(1), "team-a"))
	got, err := repo.ListGroups(context.Background())
	require.NoError(t, err)
	require.Len(t, got, 1)
	require.Equal(t, "team-a", got[0].Name)
	require.NoError(t, mock.ExpectationsWereMet())
}
```

- [ ] **Step 2: 运行确认失败**

Run: `cd backend && go test ./internal/repository/ -run 'TestOperationImageReport(TodayBreakdown|List|Distinct)'`
Expected: FAIL（方法 undefined）

- [ ] **Step 3: 实现 5 个方法**

Append to `operation_image_report_repo.go`:

```go
func (r *operationImageReportRepository) TodayBreakdown(ctx context.Context, start, end time.Time) ([]service.ImageTodayItem, error) {
	if r.sql == nil {
		return nil, errors.New("operation image report repository: nil db")
	}
	query := fmt.Sprintf(`
WITH base AS (
  SELECT a.platform AS platform, ul.model AS model, ul.group_id AS group_id,
         g.name AS group_name, (ul.actual_cost > 0) AS success
  FROM usage_logs ul
  JOIN accounts a ON a.id = ul.account_id
  LEFT JOIN groups g ON g.id = ul.group_id
  WHERE ul.created_at >= $1 AND ul.created_at < $2
    AND %s
)
SELECT 'platform' AS dimension, platform AS key, NULL::bigint AS group_id,
       COUNT(*) FILTER (WHERE success) AS success, COUNT(*) FILTER (WHERE NOT success) AS failure
FROM base GROUP BY platform
UNION ALL
SELECT 'model', model, NULL::bigint,
       COUNT(*) FILTER (WHERE success), COUNT(*) FILTER (WHERE NOT success)
FROM base GROUP BY model
UNION ALL
SELECT 'group', COALESCE(group_name, 'ungrouped'), group_id,
       COUNT(*) FILTER (WHERE success), COUNT(*) FILTER (WHERE NOT success)
FROM base GROUP BY group_id, group_name
ORDER BY dimension, key`, imageModelWhere(""))

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

func (r *operationImageReportRepository) ListPlatformAccountConcurrency(ctx context.Context, platform string) ([]service.ImageAccountConcurrency, error) {
	if r.sql == nil {
		return nil, errors.New("operation image report repository: nil db")
	}
	rows, err := r.sql.QueryContext(ctx, `
SELECT id, concurrency FROM accounts
WHERE deleted_at IS NULL AND status = 'active' AND platform = $1`, platform)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	return scanAccountConcurrency(rows)
}

func (r *operationImageReportRepository) ListAlertAccountConcurrency(ctx context.Context) ([]service.ImageAccountConcurrency, error) {
	if r.sql == nil {
		return nil, errors.New("operation image report repository: nil db")
	}
	rows, err := r.sql.QueryContext(ctx, `
SELECT id, concurrency FROM accounts
WHERE deleted_at IS NULL AND status = 'active' AND platform = 'openai' AND type = 'oauth'
  AND (
    ((extra->>'codex_5h_used_percent') ~ '^[0-9]+(\.[0-9]+)?$' AND (extra->>'codex_5h_used_percent')::float8 >= 90)
    OR ((extra->>'codex_7d_used_percent') ~ '^[0-9]+(\.[0-9]+)?$' AND (extra->>'codex_7d_used_percent')::float8 >= 90)
  )`)
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
```

- [ ] **Step 4: 运行确认通过 + 接口完整性编译**

Run: `cd backend && go test ./internal/repository/ -run TestOperationImageReport && go build ./internal/...`
Expected: PASS，且 `go build` 通过（证明 `*operationImageReportRepository` 完整实现了 `service.OperationImageReportRepository`）。
若 `groups` 无 `deleted_at` 列致集成报错，改为 `WHERE status <> 'deleted'` 或去掉过滤（Task 7 集成测试会暴露）。

- [ ] **Step 5: 提交**

```bash
git add backend/internal/repository/operation_image_report_repo.go backend/internal/repository/operation_image_report_repo_test.go
git commit -m "feat(operation): add today breakdown, account-concurrency and filter queries"
```

---

### Task 5: service 层组装逻辑单元测试

**Files:**
- Test: `backend/internal/service/operation_image_report_service_test.go`

**Interfaces:**
- Consumes: `OperationImageReportService` 全部方法、`accountConcurrencyReader`。
- Produces: 一个 `fakeImageReportRepo`（实现 `OperationImageReportRepository`）与 `fakeConcurrency` 测试替身。

注：`Concurrency()` 依赖 `accountConcurrencyReader` 而非 `*ConcurrencyService` 具体类型，故测试用 `newOperationImageReportServiceForTest(repo, reader)` 直接注入替身（见 Step 3 新增的测试构造器）。

- [ ] **Step 1: 写失败测试**

Create `backend/internal/service/operation_image_report_service_test.go`:

```go
package service

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

type fakeImageReportRepo struct {
	platformAccts map[string][]ImageAccountConcurrency
	alertAccts    []ImageAccountConcurrency
	reqBuckets    []ImageRequestBucket
}

func (f *fakeImageReportRepo) LatencySeries(context.Context, ImageReportSeriesFilter) ([]ImageLatencyBucket, error) {
	return nil, nil
}
func (f *fakeImageReportRepo) RequestSeries(context.Context, ImageReportSeriesFilter) ([]ImageRequestBucket, error) {
	return f.reqBuckets, nil
}
func (f *fakeImageReportRepo) TodayBreakdown(context.Context, time.Time, time.Time) ([]ImageTodayItem, error) {
	return nil, nil
}
func (f *fakeImageReportRepo) ListPlatformAccountConcurrency(_ context.Context, p string) ([]ImageAccountConcurrency, error) {
	return f.platformAccts[p], nil
}
func (f *fakeImageReportRepo) ListAlertAccountConcurrency(context.Context) ([]ImageAccountConcurrency, error) {
	return f.alertAccts, nil
}
func (f *fakeImageReportRepo) DistinctImageModels(context.Context, string) ([]string, error) {
	return nil, nil
}
func (f *fakeImageReportRepo) ListGroups(context.Context) ([]ImageReportGroupRef, error) {
	return nil, nil
}

type fakeConcurrency struct {
	values map[int64]int
	err    error
}

func (f *fakeConcurrency) GetAccountConcurrencyBatch(_ context.Context, ids []int64) (map[int64]int, error) {
	if f.err != nil {
		return nil, f.err
	}
	out := map[int64]int{}
	for _, id := range ids {
		out[id] = f.values[id]
	}
	return out, nil
}

func TestBuildSeriesFilterNormalizes(t *testing.T) {
	s := newOperationImageReportServiceForTest(&fakeImageReportRepo{}, &fakeConcurrency{})
	now := time.Date(2025, 1, 2, 10, 0, 0, 0, time.UTC)
	f := s.BuildSeriesFilter("GEMINI", "  m  ", nil, "garbage", "Bad/Zone", now)
	require.Equal(t, OperationPlatformGemini, f.Platform)
	require.Equal(t, "m", f.Model)
	require.Equal(t, "1h", f.Bucket)
	require.Equal(t, "UTC", f.TZ)
	require.Equal(t, now, f.End)
	require.Equal(t, now.Add(-24*time.Hour), f.Start)
}

func TestRequestSeriesComputesSuccessRate(t *testing.T) {
	repo := &fakeImageReportRepo{reqBuckets: []ImageRequestBucket{{SuccessCount: 8, FailureCount: 2}, {SuccessCount: 0, FailureCount: 0}}}
	s := newOperationImageReportServiceForTest(repo, &fakeConcurrency{})
	got, err := s.RequestSeries(context.Background(), ImageReportSeriesFilter{})
	require.NoError(t, err)
	require.InDelta(t, 0.8, got[0].SuccessRate, 1e-9)
	require.Equal(t, 0.0, got[1].SuccessRate)
}

func TestConcurrencyAggregatesAndAlerts(t *testing.T) {
	repo := &fakeImageReportRepo{
		platformAccts: map[string][]ImageAccountConcurrency{
			OperationPlatformOpenAI: {{AccountID: 1, Concurrency: 3}, {AccountID: 2, Concurrency: 5}},
			OperationPlatformGemini: {{AccountID: 3, Concurrency: 4}},
		},
		alertAccts: []ImageAccountConcurrency{{AccountID: 1, Concurrency: 3}},
	}
	conc := &fakeConcurrency{values: map[int64]int{1: 2, 2: 1, 3: 0}}
	s := newOperationImageReportServiceForTest(repo, conc)
	ov, err := s.Concurrency(context.Background())
	require.NoError(t, err)
	require.Len(t, ov.Cards, 2)
	require.Equal(t, OperationPlatformOpenAI, ov.Cards[0].Platform)
	require.Equal(t, 8, ov.Cards[0].TotalConcurrency)
	require.Equal(t, 3, ov.Cards[0].CurrentConcurrency)
	require.True(t, ov.Cards[0].Available)
	require.Equal(t, 1, ov.Alert.AccountCount)
	require.Equal(t, 3, ov.Alert.TotalConcurrency)
	require.Equal(t, 2, ov.Alert.CurrentConcurrency)
}

func TestConcurrencyDegradesWhenRedisFails(t *testing.T) {
	repo := &fakeImageReportRepo{
		platformAccts: map[string][]ImageAccountConcurrency{
			OperationPlatformOpenAI: {{AccountID: 1, Concurrency: 3}},
		},
	}
	s := newOperationImageReportServiceForTest(repo, &fakeConcurrency{err: errors.New("redis down")})
	ov, err := s.Concurrency(context.Background())
	require.NoError(t, err)
	require.False(t, ov.Cards[0].Available)
	require.Equal(t, 0, ov.Cards[0].CurrentConcurrency)
	require.Equal(t, 3, ov.Cards[0].TotalConcurrency)
}
```

- [ ] **Step 2: 运行确认失败**

Run: `cd backend && go test ./internal/service/ -run 'TestBuildSeriesFilter|TestRequestSeriesComputes|TestConcurrency'`
Expected: FAIL（`newOperationImageReportServiceForTest` undefined）

- [ ] **Step 3: 加测试构造器到生产文件**

Append to `backend/internal/service/operation_image_report_service.go`:

```go
// newOperationImageReportServiceForTest 用测试替身注入并发读取器（生产用 NewOperationImageReportService）。
func newOperationImageReportServiceForTest(repo OperationImageReportRepository, reader accountConcurrencyReader) *OperationImageReportService {
	return &OperationImageReportService{repo: repo, concurrency: reader}
}
```

- [ ] **Step 4: 运行确认通过**

Run: `cd backend && go test ./internal/service/ -run 'TestBuildSeriesFilter|TestRequestSeriesComputes|TestConcurrency'`
Expected: PASS

- [ ] **Step 5: 提交**

```bash
git add backend/internal/service/operation_image_report_service.go backend/internal/service/operation_image_report_service_test.go
git commit -m "test(operation): cover image-report service assembly and concurrency degradation"
```

---

### Task 6: HTTP handler

**Files:**
- Create: `backend/internal/handler/admin/operation_image_report_handler.go`
- Test: `backend/internal/handler/admin/operation_image_report_handler_test.go`

**Interfaces:**
- Consumes: `*service.OperationImageReportService`、`response.Success`/`response.ErrorFrom`。
- Produces: `NewOperationImageReportHandler(svc) *OperationImageReportHandler`，方法 `Overview/Concurrency/LatencySeries/RequestSeries/Filters(c *gin.Context)`。

注：handler 直接持有 `*service.OperationImageReportService` 具体类型；测试通过真实 service + `fakeImageReportRepo`（service 包内未导出，故 handler 测试改用真实 service + 一个最小的导出测试桩）。为可测，handler 仅做参数解析与转发，核心逻辑已在 service/repo 层覆盖；此处用 httptest 验证路由解析与 200/JSON 包裹。

- [ ] **Step 1: 写 handler（先写实现，再写 httptest 测试——handler 是薄转发层）**

Create `backend/internal/handler/admin/operation_image_report_handler.go`:

```go
package admin

import (
	"strconv"
	"strings"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/pkg/response"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
)

type OperationImageReportHandler struct {
	svc *service.OperationImageReportService
}

func NewOperationImageReportHandler(svc *service.OperationImageReportService) *OperationImageReportHandler {
	return &OperationImageReportHandler{svc: svc}
}

func (h *OperationImageReportHandler) parseGroupID(c *gin.Context) *int64 {
	raw := strings.TrimSpace(c.Query("group_id"))
	if raw == "" {
		return nil
	}
	id, err := strconv.ParseInt(raw, 10, 64)
	if err != nil {
		return nil
	}
	return &id
}

func (h *OperationImageReportHandler) seriesFilter(c *gin.Context) service.ImageReportSeriesFilter {
	return h.svc.BuildSeriesFilter(c.Query("platform"), c.Query("model"), h.parseGroupID(c), c.Query("bucket"), c.Query("tz"), time.Now())
}

// GET /admin/operation/image-report/overview
func (h *OperationImageReportHandler) Overview(c *gin.Context) {
	data, err := h.svc.TodayBreakdown(c.Request.Context(), c.Query("tz"), time.Now())
	if err != nil {
		response.ErrorFrom(c, err)
		return
	}
	response.Success(c, gin.H{"today": data})
}

// GET /admin/operation/image-report/concurrency
func (h *OperationImageReportHandler) Concurrency(c *gin.Context) {
	data, err := h.svc.Concurrency(c.Request.Context())
	if err != nil {
		response.ErrorFrom(c, err)
		return
	}
	response.Success(c, data)
}

// GET /admin/operation/image-report/latency-series
func (h *OperationImageReportHandler) LatencySeries(c *gin.Context) {
	data, err := h.svc.LatencySeries(c.Request.Context(), h.seriesFilter(c))
	if err != nil {
		response.ErrorFrom(c, err)
		return
	}
	response.Success(c, gin.H{"buckets": data})
}

// GET /admin/operation/image-report/request-series
func (h *OperationImageReportHandler) RequestSeries(c *gin.Context) {
	data, err := h.svc.RequestSeries(c.Request.Context(), h.seriesFilter(c))
	if err != nil {
		response.ErrorFrom(c, err)
		return
	}
	response.Success(c, gin.H{"buckets": data})
}

// GET /admin/operation/image-report/filters
func (h *OperationImageReportHandler) Filters(c *gin.Context) {
	data, err := h.svc.FilterOptions(c.Request.Context(), c.Query("platform"))
	if err != nil {
		response.ErrorFrom(c, err)
		return
	}
	response.Success(c, data)
}
```

- [ ] **Step 2: 写 handler 解析单元测试（不依赖 service 内部，验证 group_id 解析）**

Create `backend/internal/handler/admin/operation_image_report_handler_test.go`:

```go
package admin

import (
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

func TestOperationImageReportParseGroupID(t *testing.T) {
	gin.SetMode(gin.TestMode)
	h := &OperationImageReportHandler{}

	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	c.Request = httptest.NewRequest("GET", "/?group_id=42", nil)
	got := h.parseGroupID(c)
	require.NotNil(t, got)
	require.Equal(t, int64(42), *got)

	c2, _ := gin.CreateTestContext(httptest.NewRecorder())
	c2.Request = httptest.NewRequest("GET", "/?group_id=abc", nil)
	require.Nil(t, h.parseGroupID(c2))

	c3, _ := gin.CreateTestContext(httptest.NewRecorder())
	c3.Request = httptest.NewRequest("GET", "/", nil)
	require.Nil(t, h.parseGroupID(c3))
}
```

- [ ] **Step 3: 运行测试**

Run: `cd backend && go test ./internal/handler/admin/ -run TestOperationImageReportParseGroupID`
Expected: PASS（首次即应通过，因实现已写；若 FAIL 检查 import）

- [ ] **Step 4: 提交**

```bash
git add backend/internal/handler/admin/operation_image_report_handler.go backend/internal/handler/admin/operation_image_report_handler_test.go
git commit -m "feat(operation): add image-report admin HTTP handler"
```

---

### Task 7: 路由注册 + wire 接线 + 编译

**Files:**
- Create: `backend/internal/server/routes/admin_operation.go`
- Modify: `backend/internal/server/routes/admin.go`
- Modify: `backend/internal/handler/handler.go`
- Modify: `backend/internal/handler/wire.go`
- Modify: `backend/internal/repository/wire.go`
- Modify: `backend/internal/service/wire.go`
- Regenerate: `backend/cmd/server/wire_gen.go`

**Interfaces:**
- Consumes: `h.Admin.OperationImageReport`、`NewOperationImageReportRepository`、`NewOperationImageReportService`、`NewOperationImageReportHandler`。

- [ ] **Step 1: 新增路由文件**

Create `backend/internal/server/routes/admin_operation.go`:

```go
package routes

import (
	"github.com/Wei-Shaw/sub2api/internal/handler"

	"github.com/gin-gonic/gin"
)

// registerAdminOperationRoutes 注册「运营管理」相关只读路由。
func registerAdminOperationRoutes(admin *gin.RouterGroup, h *handler.Handlers) {
	op := admin.Group("/operation/image-report")
	{
		op.GET("/overview", h.Admin.OperationImageReport.Overview)
		op.GET("/concurrency", h.Admin.OperationImageReport.Concurrency)
		op.GET("/latency-series", h.Admin.OperationImageReport.LatencySeries)
		op.GET("/request-series", h.Admin.OperationImageReport.RequestSeries)
		op.GET("/filters", h.Admin.OperationImageReport.Filters)
	}
}
```

- [ ] **Step 2: 在 admin.go 注册**

In `backend/internal/server/routes/admin.go`, 在 `registerAffiliateRoutes(admin, h)` 之后（约 line 105）新增：

```go
		// 邀请返利（专属用户管理）
		registerAffiliateRoutes(admin, h)

		// 运营管理（生图报表）
		registerAdminOperationRoutes(admin, h)
```

- [ ] **Step 3: handler.go 加字段**

In `backend/internal/handler/handler.go`, 在 `AdminHandlers` 结构体（`Compliance` 字段后）新增：

```go
	Compliance             *admin.ComplianceHandler
	OperationImageReport   *admin.OperationImageReportHandler
```

- [ ] **Step 4: wire.go ProvideAdminHandlers 加参数与赋值**

In `backend/internal/handler/wire.go`, 给 `ProvideAdminHandlers` 增加参数 `operationImageReportHandler *admin.OperationImageReportHandler`，并在返回结构体中新增 `OperationImageReport: operationImageReportHandler,`。

- [ ] **Step 5: 注册 repo/service 构造函数到 ProviderSet**

In `backend/internal/repository/wire.go` 的 `ProviderSet` 中新增 `NewOperationImageReportRepository,`。
In `backend/internal/service/wire.go` 的 `ProviderSet` 中新增 `NewOperationImageReportService,`。
In `backend/internal/handler/wire.go`（或 handler 的 ProviderSet 所在处）新增 `admin.NewOperationImageReportHandler,`（参照 `admin.NewOpsHandler` 已注册的位置）。

- [ ] **Step 6: 重新生成 wire**

Run: `cd backend && go run github.com/google/wire/cmd/wire ./...`
Expected: 生成更新后的 `cmd/server/wire_gen.go`（出现 `repository.NewOperationImageReportRepository`、`service.NewOperationImageReportService`、`admin.NewOperationImageReportHandler` 的实例化）。
若 `wire` 不可用，手动在 `wire_gen.go` 中按依赖顺序补：
```go
operationImageReportRepository := repository.NewOperationImageReportRepository(db)
operationImageReportService := service.NewOperationImageReportService(operationImageReportRepository, concurrencyService)
operationImageReportHandler := admin.NewOperationImageReportHandler(operationImageReportService)
```
并把 `operationImageReportHandler` 传入 `handler.ProvideAdminHandlers(...)` 调用处。（变量名 `db`、`concurrencyService` 以 `wire_gen.go` 现有命名为准。）

- [ ] **Step 7: 编译 + vet + 全量后端测试**

Run: `cd backend && go build ./... && go vet ./internal/... && go test ./internal/repository/ ./internal/service/ ./internal/handler/admin/ -run Operation`
Expected: 全部通过。

- [ ] **Step 8: 提交**

```bash
git add backend/internal/server/routes/admin_operation.go backend/internal/server/routes/admin.go backend/internal/handler/handler.go backend/internal/handler/wire.go backend/internal/repository/wire.go backend/internal/service/wire.go backend/cmd/server/wire_gen.go
git commit -m "feat(operation): wire image-report routes, handler, service and repository"
```

---

### Task 8: 集成测试（真实 PostgreSQL，验证 SQL 正确性）

**Files:**
- Test: `backend/internal/repository/operation_image_report_repo_integration_test.go`

**Interfaces:**
- Consumes: harness 全局 `integrationDB`（package repository，`//go:build integration`）。

说明：sqlmock 不执行真实 SQL，无法发现语法错误；本任务用 testcontainers 真库验证聚合结果。需本地 Docker；无 Docker 时该套件自动跳过（harness 设计）。

- [ ] **Step 1: 写集成测试**

Create `backend/internal/repository/operation_image_report_repo_integration_test.go`:

```go
//go:build integration

package repository

import (
	"context"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/stretchr/testify/require"
)

func seedImageReportData(t *testing.T, ctx context.Context) {
	t.Helper()
	// 一个 openai 账号（高用量告警），一个 gemini 账号。
	_, err := integrationDB.ExecContext(ctx, `
INSERT INTO accounts (id, name, platform, type, credentials, extra, concurrency, status, created_at, updated_at)
VALUES
 (9001, 'oa', 'openai', 'oauth', '{}'::jsonb, '{"codex_5h_used_percent":"95"}'::jsonb, 4, 'active', NOW(), NOW()),
 (9002, 'ge', 'gemini', 'oauth', '{}'::jsonb, '{}'::jsonb, 6, 'active', NOW(), NOW())
ON CONFLICT (id) DO NOTHING`)
	require.NoError(t, err)

	now := time.Now().UTC()
	// 成功生图(openai, actual_cost>0)、失败生图(actual_cost=0)、gemini 成功生图。
	_, err = integrationDB.ExecContext(ctx, `
INSERT INTO usage_logs (user_id, api_key_id, account_id, request_id, model, requested_model, actual_cost, total_cost, duration_ms, image_count, created_at)
VALUES
 (1,1,9001,$1,'gpt-image-2','gpt-image-2', 0.5, 0.5, 1200, 1, $4),
 (1,1,9001,$2,'gpt-image-2','gpt-image-2', 0.0, 0.0, NULL, 0, $4),
 (1,1,9002,$3,'gemini-3-pro-image','gemini-3-pro-image', 0.3, 0.3, 800, 1, $4)`,
		"req-a", "req-b", "req-c", now)
	require.NoError(t, err)
}

func TestOperationImageReportIntegration(t *testing.T) {
	ctx := context.Background()
	repo := &operationImageReportRepository{sql: integrationDB}
	seedImageReportData(t, ctx)

	start := time.Now().UTC().Add(-24 * time.Hour)
	end := time.Now().UTC().Add(time.Hour)

	// request-series: openai 1 成功 1 失败
	req, err := repo.RequestSeries(ctx, service.ImageReportSeriesFilter{
		Platform: service.OperationPlatformOpenAI, Bucket: "1h", TZ: "UTC", Start: start, End: end,
	})
	require.NoError(t, err)
	var succ, fail int64
	for _, b := range req {
		succ += b.SuccessCount
		fail += b.FailureCount
	}
	require.Equal(t, int64(1), succ)
	require.Equal(t, int64(1), fail)

	// latency-series: 仅成功 openai 一条
	lat, err := repo.LatencySeries(ctx, service.ImageReportSeriesFilter{
		Platform: service.OperationPlatformOpenAI, Bucket: "1h", TZ: "UTC", Start: start, End: end,
	})
	require.NoError(t, err)
	var cnt int64
	for _, b := range lat {
		cnt += b.Count
	}
	require.Equal(t, int64(1), cnt)

	// today: 含 platform/model/group 维度，至少 openai 平台行
	today, err := repo.TodayBreakdown(ctx, start, end)
	require.NoError(t, err)
	require.NotEmpty(t, today)

	// alert: openai 高用量账号 9001
	alert, err := repo.ListAlertAccountConcurrency(ctx)
	require.NoError(t, err)
	var found bool
	for _, a := range alert {
		if a.AccountID == 9001 {
			found = true
			require.Equal(t, 4, a.Concurrency)
		}
	}
	require.True(t, found)

	// platform accounts + filters
	openaiAccts, err := repo.ListPlatformAccountConcurrency(ctx, "openai")
	require.NoError(t, err)
	require.NotEmpty(t, openaiAccts)
	models, err := repo.DistinctImageModels(ctx, "openai")
	require.NoError(t, err)
	require.Contains(t, models, "gpt-image-2")
	_, err = repo.ListGroups(ctx)
	require.NoError(t, err)
}
```

- [ ] **Step 2: 运行集成测试（需 Docker）**

Run: `cd backend && go test -tags=integration ./internal/repository/ -run TestOperationImageReportIntegration -v`
Expected: PASS（若无 Docker：`docker is not available; skipping` 并退出 0 —— 在有 Docker 的机器上必须真实通过；修正 SQL 直到通过，尤其 `groups.deleted_at`、`accounts` 列名）。

- [ ] **Step 3: 提交**

```bash
git add backend/internal/repository/operation_image_report_repo_integration_test.go
git commit -m "test(operation): integration-test image-report queries against real postgres"
```

---

## Phase 2 — 前端

### Task 9: 前端 API 封装 + 类型

**Files:**
- Create: `frontend/src/api/admin/operationImageReport.ts`
- Modify: `frontend/src/api/admin/index.ts`
- Test: `frontend/src/api/admin/__tests__/operationImageReport.spec.ts`

**Interfaces:**
- Produces: `operationImageReportAPI`（`concurrency/overview/latencySeries/requestSeries/filters`）；类型 `ConcurrencyOverview`、`LatencyBucket`、`RequestBucket`、`TodayItem`、`FilterOptions`。
- Consumes: `apiClient`（响应拦截器已解包 `data`）。

- [ ] **Step 1: 写 API 模块**

Create `frontend/src/api/admin/operationImageReport.ts`:

```typescript
/**
 * Admin 运营管理 - 生图报表 API
 * 全部只读 GET。
 */
import { apiClient } from '../client'

export interface ConcurrencyCard {
  platform: string
  current_concurrency: number
  total_concurrency: number
  available: boolean
}
export interface AlertConcurrency {
  account_count: number
  current_concurrency: number
  total_concurrency: number
  available: boolean
}
export interface ConcurrencyOverview {
  cards: ConcurrencyCard[]
  alert: AlertConcurrency
}
export interface LatencyBucket {
  bucket_start: string
  count: number
  min_ms: number | null
  p25_ms: number | null
  p50_ms: number | null
  p75_ms: number | null
  max_ms: number | null
  avg_ms: number | null
}
export interface RequestBucket {
  bucket_start: string
  success_count: number
  failure_count: number
  success_rate: number
}
export interface TodayItem {
  dimension: 'platform' | 'model' | 'group'
  key: string
  group_id?: number
  success: number
  failure: number
}
export interface FilterOptions {
  models: string[]
  groups: { id: number; name: string }[]
}

export interface SeriesParams {
  platform?: string
  model?: string
  group_id?: number
  bucket?: '5m' | '1h'
  tz?: string
}

const base = '/admin/operation/image-report'

const operationImageReportAPI = {
  async concurrency(): Promise<ConcurrencyOverview> {
    const { data } = await apiClient.get<ConcurrencyOverview>(`${base}/concurrency`)
    return data
  },
  async overview(tz: string): Promise<{ today: TodayItem[] }> {
    const { data } = await apiClient.get<{ today: TodayItem[] }>(`${base}/overview`, { params: { tz } })
    return data
  },
  async latencySeries(params: SeriesParams): Promise<{ buckets: LatencyBucket[] }> {
    const { data } = await apiClient.get<{ buckets: LatencyBucket[] }>(`${base}/latency-series`, { params })
    return data
  },
  async requestSeries(params: SeriesParams): Promise<{ buckets: RequestBucket[] }> {
    const { data } = await apiClient.get<{ buckets: RequestBucket[] }>(`${base}/request-series`, { params })
    return data
  },
  async filters(platform: string): Promise<FilterOptions> {
    const { data } = await apiClient.get<FilterOptions>(`${base}/filters`, { params: { platform } })
    return data
  }
}

export default operationImageReportAPI
```

- [ ] **Step 2: 在 admin/index.ts 注册**

In `frontend/src/api/admin/index.ts`: import + 挂入 `adminAPI`：

```typescript
import operationImageReportAPI from './operationImageReport'
// ...
export const adminAPI = {
  // ... 现有项
  operationImageReport: operationImageReportAPI,
}
```

- [ ] **Step 3: 写 API 单元测试（mock apiClient）**

Create `frontend/src/api/admin/__tests__/operationImageReport.spec.ts`:

```typescript
import { describe, it, expect, vi, beforeEach } from 'vitest'
import operationImageReportAPI from '../operationImageReport'
import { apiClient } from '../../client'

vi.mock('../../client', () => ({
  apiClient: { get: vi.fn() }
}))

describe('operationImageReportAPI', () => {
  beforeEach(() => vi.clearAllMocks())

  it('requests concurrency endpoint and unwraps data', async () => {
    ;(apiClient.get as any).mockResolvedValue({ data: { cards: [], alert: {} } })
    const res = await operationImageReportAPI.concurrency()
    expect(apiClient.get).toHaveBeenCalledWith('/admin/operation/image-report/concurrency')
    expect(res).toEqual({ cards: [], alert: {} })
  })

  it('passes series params through', async () => {
    ;(apiClient.get as any).mockResolvedValue({ data: { buckets: [] } })
    await operationImageReportAPI.requestSeries({ platform: 'openai', bucket: '5m', tz: 'UTC' })
    expect(apiClient.get).toHaveBeenCalledWith('/admin/operation/image-report/request-series', {
      params: { platform: 'openai', bucket: '5m', tz: 'UTC' }
    })
  })
})
```

- [ ] **Step 4: 运行测试**

Run: `cd frontend && pnpm vitest run src/api/admin/__tests__/operationImageReport.spec.ts`
Expected: PASS

- [ ] **Step 5: 提交**

```bash
git add frontend/src/api/admin/operationImageReport.ts frontend/src/api/admin/index.ts frontend/src/api/admin/__tests__/operationImageReport.spec.ts
git commit -m "feat(operation): add frontend image-report api client"
```

---

### Task 10: i18n + 路由 + 侧边栏菜单

**Files:**
- Modify: `frontend/src/i18n/locales/zh.ts`
- Modify: `frontend/src/i18n/locales/en.ts`
- Modify: `frontend/src/router/index.ts`
- Modify: `frontend/src/components/layout/AppSidebar.vue`
- Create: `frontend/src/views/admin/operation/ImageReportView.vue`（占位，Task 11 充实）

**Interfaces:**
- Produces: 路由 `AdminOperationImageReport`（`/admin/operation/image-report`）；i18n key `nav.operations`、`nav.imageReport`、`admin.operation.imageReport.*`。

- [ ] **Step 1: 占位页面**

Create `frontend/src/views/admin/operation/ImageReportView.vue`:

```vue
<template>
  <AppLayout>
    <div class="space-y-6">
      <h1 class="text-2xl font-bold text-gray-900 dark:text-white">
        {{ t('admin.operation.imageReport.title') }}
      </h1>
    </div>
  </AppLayout>
</template>

<script setup lang="ts">
import { useI18n } from 'vue-i18n'
import AppLayout from '@/components/layout/AppLayout.vue'
const { t } = useI18n()
</script>
```

- [ ] **Step 2: i18n（zh.ts / en.ts）**

In `frontend/src/i18n/locales/zh.ts` 的 `nav` 段新增：
```typescript
    operations: '运营管理',
    imageReport: '生图报表',
```
并在 `admin` 段新增子树：
```typescript
    operation: {
      imageReport: {
        title: '生图报表',
        description: 'OpenAI / Gemini 生图调用的运营监控',
        currentConcurrency: '当前并发',
        totalConcurrency: '总并发',
        alertConcurrency: '告警并发',
        alertHint: '5h 或 7d 用量 ≥ 90% 的 OpenAI 账号',
        today: '今日生图',
        success: '成功',
        failure: '失败',
        successRate: '成功率',
        latency: '成功耗时',
        requestVolume: '请求量',
        platform: '平台',
        model: '模型',
        group: '分组',
        granularity: '粒度',
        unavailable: '暂不可用',
        successRateNote: '失败仅统计已落库的请求，成功率可能偏高'
      }
    },
```
In `frontend/src/i18n/locales/en.ts` 对应英文：
```typescript
    operations: 'Operations',
    imageReport: 'Image Report',
```
```typescript
    operation: {
      imageReport: {
        title: 'Image Report',
        description: 'Operational monitoring of OpenAI / Gemini image generation',
        currentConcurrency: 'Current',
        totalConcurrency: 'Total',
        alertConcurrency: 'At-risk concurrency',
        alertHint: 'OpenAI accounts with 5h or 7d usage ≥ 90%',
        today: 'Today',
        success: 'Success',
        failure: 'Failure',
        successRate: 'Success rate',
        latency: 'Success latency',
        requestVolume: 'Requests',
        platform: 'Platform',
        model: 'Model',
        group: 'Group',
        granularity: 'Granularity',
        unavailable: 'Unavailable',
        successRateNote: 'Failures count only logged requests; success rate may be optimistic'
      }
    },
```

- [ ] **Step 3: 路由**

In `frontend/src/router/index.ts`，在 admin 路由块（参照 `/admin/ops` 条目附近）新增：
```typescript
  {
    path: '/admin/operation/image-report',
    name: 'AdminOperationImageReport',
    component: () => import('@/views/admin/operation/ImageReportView.vue'),
    meta: {
      requiresAuth: true,
      requiresAdmin: true,
      title: 'Image Report',
      titleKey: 'admin.operation.imageReport.title',
      descriptionKey: 'admin.operation.imageReport.description'
    }
  },
```

- [ ] **Step 4: 侧边栏一级菜单**

In `frontend/src/components/layout/AppSidebar.vue` 的 `adminNavItems` computed 的 `baseItems` 中新增（放在 Usage 之前或合适位置，参照 Channel Management 的 `expandOnly` 结构）：
```typescript
      {
        path: '/admin/operation',
        label: t('nav.operations'),
        icon: ChartIcon,
        hideInSimpleMode: true,
        expandOnly: true,
        children: [
          { path: '/admin/operation/image-report', label: t('nav.imageReport'), icon: ChartIcon }
        ]
      },
```
（`ChartIcon` 已在该文件内定义并被使用；复用即可。）

- [ ] **Step 5: 构建/类型检查 + 启动验证菜单出现**

Run: `cd frontend && pnpm build`
Expected: 构建通过（类型检查无误）。手动：登录 admin，侧边栏出现「运营管理 ▸ 生图报表」，点击进入占位页标题正确。

- [ ] **Step 6: 提交**

```bash
git add frontend/src/i18n/locales/zh.ts frontend/src/i18n/locales/en.ts frontend/src/router/index.ts frontend/src/components/layout/AppSidebar.vue frontend/src/views/admin/operation/ImageReportView.vue
git commit -m "feat(operation): add operations menu, route and i18n for image report"
```

---

### Task 11: 图表/卡片子组件 + 页面装配

**Files:**
- Create: `frontend/src/views/admin/operation/components/ConcurrencyCards.vue`
- Create: `frontend/src/views/admin/operation/components/TodayBreakdown.vue`
- Create: `frontend/src/views/admin/operation/components/LatencyChart.vue`
- Create: `frontend/src/views/admin/operation/components/RequestVolumeChart.vue`
- Modify: `frontend/src/views/admin/operation/ImageReportView.vue`
- Test: `frontend/src/views/admin/operation/components/__tests__/LatencyChart.spec.ts`

**Interfaces:**
- Consumes: `operationImageReportAPI`、上面定义的类型、chart.js/vue-chartjs（参照 `views/admin/ops/components/OpsLatencyChart.vue`）。

- [ ] **Step 1: LatencyChart 组件（多线：min/p25/p50/p75/max/avg）**

Create `frontend/src/views/admin/operation/components/LatencyChart.vue`（结构参照 `OpsLatencyChart.vue`：`ChartJS.register(LineElement, PointElement, CategoryScale, LinearScale, Tooltip, Legend)`；`Line` 组件）：

```vue
<template>
  <div class="card p-6">
    <h3 class="mb-4 text-sm font-bold text-gray-900 dark:text-white">{{ t('admin.operation.imageReport.latency') }}</h3>
    <div class="h-72">
      <Line v-if="chartData" :data="chartData" :options="options" />
      <div v-else class="flex h-full items-center justify-center text-sm text-gray-400">{{ t('common.noData') }}</div>
    </div>
  </div>
</template>

<script setup lang="ts">
import { computed } from 'vue'
import { useI18n } from 'vue-i18n'
import { Line } from 'vue-chartjs'
import { Chart as ChartJS, LineElement, PointElement, CategoryScale, LinearScale, Tooltip, Legend } from 'chart.js'
import type { LatencyBucket } from '@/api/admin/operationImageReport'

ChartJS.register(LineElement, PointElement, CategoryScale, LinearScale, Tooltip, Legend)

const props = defineProps<{ buckets: LatencyBucket[] }>()
const { t } = useI18n()

const series: { key: keyof LatencyBucket; label: string; color: string }[] = [
  { key: 'min_ms', label: 'min', color: '#9ca3af' },
  { key: 'p25_ms', label: 'p25', color: '#60a5fa' },
  { key: 'p50_ms', label: 'p50', color: '#3b82f6' },
  { key: 'p75_ms', label: 'p75', color: '#f59e0b' },
  { key: 'max_ms', label: 'max', color: '#ef4444' },
  { key: 'avg_ms', label: 'avg', color: '#10b981' }
]

const chartData = computed(() => {
  if (!props.buckets?.length) return null
  return {
    labels: props.buckets.map((b) => b.bucket_start),
    datasets: series.map((s) => ({
      label: s.label,
      data: props.buckets.map((b) => b[s.key] as number | null),
      borderColor: s.color,
      borderWidth: 2,
      pointRadius: 0,
      fill: false,
      tension: 0.3
    }))
  }
})

const options = {
  responsive: true,
  maintainAspectRatio: false,
  plugins: { legend: { display: true, position: 'top' as const } },
  scales: { y: { beginAtZero: true } }
}
</script>
```

- [ ] **Step 2: RequestVolumeChart 组件（成功/失败 + 成功率）**

Create `frontend/src/views/admin/operation/components/RequestVolumeChart.vue`（`Line`，两条计数线；成功率可作 tooltip 或第二 y 轴，先做成功/失败两线）。结构同上，`datasets` 为 `success_count`、`failure_count`。

- [ ] **Step 3: ConcurrencyCards 组件**

Create `frontend/src/views/admin/operation/components/ConcurrencyCards.vue`：props `overview: ConcurrencyOverview | null`；渲染 openai/gemini 两张卡（`current_concurrency / total_concurrency`，`available=false` 时当前显示 `t('...unavailable')`），外加告警卡（`alert.current_concurrency / alert.total_concurrency` + `account_count` + `alertHint`）。

- [ ] **Step 4: TodayBreakdown 组件**

Create `frontend/src/views/admin/operation/components/TodayBreakdown.vue`：props `items: TodayItem[]`；按 `dimension` 分 平台/模型/分组三组，每行展示 `key`、成功、失败。

- [ ] **Step 5: 装配 ImageReportView.vue**

Rewrite `frontend/src/views/admin/operation/ImageReportView.vue`：
- `filters` ref：`platform`（默认 `'openai'`）、`model`、`group_id`、`bucket`（默认 `'1h'`）。
- `tz = Intl.DateTimeFormat().resolvedOptions().timeZone`。
- `onMounted`：并行 `concurrency()`、`overview(tz)`、`filters(platform)`、`latencySeries(...)`、`requestSeries(...)`。
- 筛选变化时重新拉 series + overview。
- 模板：`<ConcurrencyCards>` → `<TodayBreakdown>` → 筛选栏 → `<LatencyChart>` + `<RequestVolumeChart>`；底部加 `successRateNote` 小字。
- 调用统一带 `tz`，series 调用带 `{ platform, model, group_id, bucket, tz }`（空值不传）。

- [ ] **Step 6: 图表组件单元测试**

Create `frontend/src/views/admin/operation/components/__tests__/LatencyChart.spec.ts`（参照 `views/admin/ops/components/__tests__/OpsErrorScopeCharts.spec.ts` 的 mount 方式）：

```typescript
import { describe, it, expect, vi } from 'vitest'
import { mount } from '@vue/test-utils'
import LatencyChart from '../LatencyChart.vue'

vi.mock('vue-chartjs', () => ({ Line: { name: 'Line', template: '<div class="mock-line" />' } }))

describe('LatencyChart', () => {
  const stubs = { 'vue-i18n': true }
  it('shows no-data when empty', () => {
    const w = mount(LatencyChart, { props: { buckets: [] }, global: { mocks: { t: (k: string) => k } } })
    expect(w.find('.mock-line').exists()).toBe(false)
  })
  it('renders chart when buckets present', () => {
    const buckets = [{ bucket_start: '2025-01-01T00:00:00Z', count: 1, min_ms: 1, p25_ms: 2, p50_ms: 3, p75_ms: 4, max_ms: 5, avg_ms: 3 }]
    const w = mount(LatencyChart, { props: { buckets }, global: { mocks: { t: (k: string) => k } } })
    expect(w.find('.mock-line').exists()).toBe(true)
  })
})
```
（若项目 vitest 已全局注入 `t`/i18n，按现有 spec 的写法调整 mock；以 `OpsErrorScopeCharts.spec.ts` 为准。）

- [ ] **Step 7: 测试 + 构建**

Run: `cd frontend && pnpm vitest run src/views/admin/operation && pnpm build`
Expected: 测试 PASS，构建通过。

- [ ] **Step 8: 端到端手动验证**

启动前后端，登录 admin → 运营管理 → 生图报表：并发卡片、今日看板、两张曲线随筛选（平台/模型/分组/粒度）更新；Redis 停掉时当前并发显示「暂不可用」而其余正常。

- [ ] **Step 9: 提交**

```bash
git add frontend/src/views/admin/operation/
git commit -m "feat(operation): build image-report dashboard view, cards and charts"
```

---

## Phase 3 — 收尾

### Task 12: 全量验证 + 文档

**Files:**
- Modify: `CLAUDE.md`（Code Maps 增加运营管理/生图报表条目，指向本 spec）

- [ ] **Step 1: 后端全量**

Run: `cd backend && go build ./... && go vet ./internal/... && go test ./internal/repository/ ./internal/service/ ./internal/handler/admin/`
Expected: 通过。（有 Docker 时再跑 `-tags=integration ... -run TestOperationImageReportIntegration`。）

- [ ] **Step 2: 前端全量**

Run: `cd frontend && pnpm build && pnpm vitest run src/api/admin src/views/admin/operation`
Expected: 通过。

- [ ] **Step 3: Code Map**

In `CLAUDE.md` 的 `## Code Maps` 下新增一行，指向 `docs/superpowers/specs/2026-06-20-operation-image-report-design.md`，列出 handler/service/repo/前端页面的 `file` 锚点。

- [ ] **Step 4: 提交**

```bash
git add CLAUDE.md
git commit -m "docs(operation): add image-report code map entry"
```

---

## Self-Review 记录

- **Spec 覆盖**：需求1（模型范围统计）→ TodayBreakdown 维度 model + filters；需求2（并发/告警卡）→ Task 5/6 Concurrency；需求3（耗时分位数曲线）→ Task 3 LatencySeries；需求4（请求量/成功率）→ Task 2 RequestSeries + service success_rate；需求5（平台/模型/分组筛选 + 粒度）→ seriesFilter + filters 端点；需求6（今日看板分平台/模型/分组×成功失败）→ Task 4 TodayBreakdown。全部有对应任务。
- **占位扫描**：无 TBD；每个 step 含真实代码或精确命令。
- **类型一致性**：service 类型名（`ImageLatencyBucket` 等）、repo 方法签名、handler 调用、前端字段（snake_case JSON）跨任务一致；`accountConcurrencyReader` 与 `*ConcurrencyService.GetAccountConcurrencyBatch` 签名匹配。
- **已知风险**：①失败请求未必落库 → 成功率偏高，UI 以 `successRateNote` 标注；②并发为账号级口径（含非生图流量），已在 spec §3.6 记录；③`groups.deleted_at`/`accounts` 列名以集成测试为准校验。
