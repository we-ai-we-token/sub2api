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
