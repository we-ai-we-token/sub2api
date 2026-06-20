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

	// Create prerequisite user and api_key for usage_logs FK constraints.
	user := mustCreateUser(t, integrationEntClient, &service.User{})
	apiKey := mustCreateApiKey(t, integrationEntClient, &service.APIKey{UserID: user.ID})

	now := time.Now().UTC()
	// 成功生图(openai, actual_cost>0)、失败生图(actual_cost=0)、gemini 成功生图。
	_, err = integrationDB.ExecContext(ctx, `
INSERT INTO usage_logs (user_id, api_key_id, account_id, request_id, model, requested_model, actual_cost, total_cost, duration_ms, image_count, image_size, created_at)
VALUES
 ($1,$2,9001,$3,'gpt-image-2','gpt-image-2', 0.5, 0.5, 1200, 1, '1K', $6),
 ($1,$2,9001,$4,'gpt-image-2','gpt-image-2', 0.0, 0.0, NULL, 0, NULL, $6),
 ($1,$2,9002,$5,'gemini-3-pro-image','gemini-3-pro-image', 0.3, 0.3, 800, 1, '1K', $6)`,
		user.ID, apiKey.ID, "req-a", "req-b", "req-c", now)
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
	var openaiPlatform *service.ImageTodayItem
	for i := range today {
		if today[i].Dimension == "platform" && today[i].Key == "openai" {
			openaiPlatform = &today[i]
		}
	}
	require.NotNil(t, openaiPlatform, "today breakdown must include the openai platform row")
	require.Equal(t, int64(1), openaiPlatform.Success)
	require.Equal(t, int64(1), openaiPlatform.Failure)

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
	grp := mustCreateGroup(t, integrationEntClient, &service.Group{Name: "img-report-test-group"})
	groups, err := repo.ListGroups(ctx)
	require.NoError(t, err)
	var foundGroup bool
	for _, g := range groups {
		if g.ID == grp.ID {
			foundGroup = true
			require.Equal(t, grp.Name, g.Name)
		}
	}
	require.True(t, foundGroup, "ListGroups must contain the seeded group")
}
