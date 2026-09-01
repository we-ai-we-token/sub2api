package repository

import (
	"context"
	"testing"
	"time"

	"github.com/DATA-DOG/go-sqlmock"
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

func TestOperationImageReportRequestSeries(t *testing.T) {
	db, mock := newSQLMock(t)
	repo := &operationImageReportRepository{sql: db}

	start := time.Date(2025, 1, 1, 0, 0, 0, 0, time.UTC)
	end := start.Add(24 * time.Hour)
	b0 := time.Date(2025, 1, 1, 1, 0, 0, 0, time.UTC)

	mock.ExpectQuery("FROM usage_logs ul").
		WithArgs("1 hour", "UTC", start, end, "", nil, nil).
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

func TestOperationImageReportLatencySeries(t *testing.T) {
	db, mock := newSQLMock(t)
	repo := &operationImageReportRepository{sql: db}

	start := time.Date(2025, 1, 1, 0, 0, 0, 0, time.UTC)
	end := start.Add(24 * time.Hour)
	b0 := time.Date(2025, 1, 1, 1, 0, 0, 0, time.UTC)
	gid := int64(7)
	uid := int64(244)

	mock.ExpectQuery("percentile_cont").
		WithArgs("5 minutes", "Asia/Shanghai", start, end, "gpt-image-2", gid, uid).
		WillReturnRows(sqlmock.NewRows([]string{"bucket_start", "cnt", "min_ms", "p25_ms", "p50_ms", "p75_ms", "max_ms", "avg_ms"}).
			AddRow(b0, int64(4), 100.0, 120.0, 150.0, 200.0, 400.0, 180.0))

	got, err := repo.LatencySeries(context.Background(), service.ImageReportSeriesFilter{
		Platform: service.OperationPlatformOpenAI, Model: "gpt-image-2", GroupID: &gid, UserID: &uid,
		Bucket: "5m", TZ: "Asia/Shanghai", Start: start, End: end,
	})
	require.NoError(t, err)
	require.Len(t, got, 1)
	require.Equal(t, int64(4), got[0].Count)
	require.NotNil(t, got[0].P50Ms)
	require.Equal(t, 150.0, *got[0].P50Ms)
	require.NoError(t, mock.ExpectationsWereMet())
}

// 分段耗时曲线读 image_generation_records（而不是 usage_logs），且必须把
// platform 作为最后一个绑定参数下推——写错参数顺序会静默返回空曲线。
func TestOperationImageReportStageLatencySeries(t *testing.T) {
	db, mock := newSQLMock(t)
	repo := &operationImageReportRepository{sql: db}

	start := time.Date(2025, 1, 1, 0, 0, 0, 0, time.UTC)
	end := start.Add(24 * time.Hour)
	b0 := time.Date(2025, 1, 1, 1, 0, 0, 0, time.UTC)
	uid := int64(244)

	mock.ExpectQuery("FROM image_generation_records").
		WithArgs("1 hour", "UTC", start, end, "", nil, uid, service.OperationPlatformOpenAI).
		WillReturnRows(sqlmock.NewRows([]string{
			"bucket_start", "cnt",
			"upstream_p50", "upstream_p90", "upstream_p95",
			"response_p50", "response_p90", "response_p95",
		}).AddRow(b0, int64(17), 28500.0, 45000.0, 49000.0, 22000.0, 150000.0, 172500.0))

	got, err := repo.StageLatencySeries(context.Background(), service.ImageReportSeriesFilter{
		Platform: service.OperationPlatformOpenAI, UserID: &uid,
		Bucket: "1h", TZ: "UTC", Start: start, End: end,
	})
	require.NoError(t, err)
	require.Len(t, got, 1)
	require.Equal(t, int64(17), got[0].Count)
	require.NotNil(t, got[0].UpstreamP50)
	require.Equal(t, 28500.0, *got[0].UpstreamP50)
	require.NotNil(t, got[0].ResponseP95)
	require.Equal(t, 172500.0, *got[0].ResponseP95)
	require.NoError(t, mock.ExpectationsWereMet())
}

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

func TestOperationImageReportListImageAccountConcurrency(t *testing.T) {
	db, mock := newSQLMock(t)
	repo := &operationImageReportRepository{sql: db}
	// adobe 类别的查询应包含渠道关联与模型限制谓词，且不带绑定参数。
	mock.ExpectQuery("FROM accounts a").
		WillReturnRows(sqlmock.NewRows([]string{"id", "concurrency"}).
			AddRow(int64(1), 3).AddRow(int64(2), 5))
	got, err := repo.ListImageAccountConcurrency(context.Background(), service.ImageAccountCategoryAdobe)
	require.NoError(t, err)
	require.Len(t, got, 2)
	require.Equal(t, 5, got[1].Concurrency)
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestOperationImageReportListImageAccountConcurrencyUnknownCategory(t *testing.T) {
	db, _ := newSQLMock(t)
	repo := &operationImageReportRepository{sql: db}
	_, err := repo.ListImageAccountConcurrency(context.Background(), "bogus")
	require.Error(t, err)
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
