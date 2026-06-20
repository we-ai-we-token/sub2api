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
	// openai oauth 生图账号（模型限制含 gpt-image-2 + 高用量告警），gemini 生图账号（模型限制含 gemini 生图模型）。
	// 「模型限制」即 credentials.model_mapping 的 key（白名单项）。
	_, err := integrationDB.ExecContext(ctx, `
INSERT INTO accounts (id, name, platform, type, credentials, extra, concurrency, status, created_at, updated_at)
VALUES
 (9001, 'oa', 'openai', 'oauth', '{"model_mapping": {"gpt-image-2": "gpt-image-2"}}'::jsonb, '{"codex_5h_used_percent":"95"}'::jsonb, 4, 'active', NOW(), NOW()),
 (9002, 'ge', 'gemini', 'oauth', '{"model_mapping": {"gemini-3-pro-image": "gemini-3-pro-image"}}'::jsonb, '{}'::jsonb, 6, 'active', NOW(), NOW())
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

	// 生图失败落在 ops_error_logs（status_code>=400），不在 usage_logs。一条 openai 生图失败。
	// error_type 用唯一标记便于清理；error_phase/error_type 为 NOT NULL 列。
	_, err = integrationDB.ExecContext(ctx, `
INSERT INTO ops_error_logs (platform, model, status_code, error_phase, error_type, created_at)
VALUES ('openai', 'gpt-image-2', 500, 'upstream', 'img-report-test', $1)`, now)
	require.NoError(t, err)

	// 直接写入共享 integrationDB 的行必须自行清理，否则会污染其它套件
	// （如 dashboard 今日统计）的精确/增量计数断言。FK 安全顺序：先删引用方。
	t.Cleanup(func() {
		_, _ = integrationDB.Exec(`DELETE FROM ops_error_logs WHERE error_type = 'img-report-test'`)
		_, _ = integrationDB.Exec(`DELETE FROM usage_logs WHERE request_id IN ('req-a', 'req-b', 'req-c')`)
		_, _ = integrationDB.Exec(`DELETE FROM accounts WHERE id IN (9001, 9002)`)
		_, _ = integrationDB.Exec(`DELETE FROM api_keys WHERE id = $1`, apiKey.ID)
		_, _ = integrationDB.Exec(`DELETE FROM users WHERE id = $1`, user.ID)
	})
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

	// image-account concurrency by category
	oauthAccts, err := repo.ListImageAccountConcurrency(ctx, service.ImageAccountCategoryOpenAIOAuth)
	require.NoError(t, err)
	requireContainsAccount(t, oauthAccts, 9001, 4)

	geminiAccts, err := repo.ListImageAccountConcurrency(ctx, service.ImageAccountCategoryGemini)
	require.NoError(t, err)
	requireContainsAccount(t, geminiAccts, 9002, 6)

	// adobe 类别走 channel JOIN，至少要能对真实 schema 执行通过（校验 JOIN 列名正确）
	_, err = repo.ListImageAccountConcurrency(ctx, service.ImageAccountCategoryAdobe)
	require.NoError(t, err)

	models, err := repo.DistinctImageModels(ctx, "openai")
	require.NoError(t, err)
	require.Contains(t, models, "gpt-image-2")
	grp := mustCreateGroup(t, integrationEntClient, &service.Group{Name: "img-report-test-group"})
	t.Cleanup(func() { _, _ = integrationDB.Exec(`DELETE FROM groups WHERE id = $1`, grp.ID) })
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

func requireContainsAccount(t *testing.T, accts []service.ImageAccountConcurrency, id int64, concurrency int) {
	t.Helper()
	for _, a := range accts {
		if a.AccountID == id {
			require.Equal(t, concurrency, a.Concurrency)
			return
		}
	}
	t.Fatalf("account %d not found in concurrency list", id)
}
