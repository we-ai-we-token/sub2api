package service

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

type fakeImageReportRepo struct {
	categoryAccts map[string][]ImageAccountConcurrency
	alertAccts    []ImageAccountConcurrency
	reqBuckets    []ImageRequestBucket
	stageBuckets  []ImageStageLatencyBucket
	lastFilter    ImageReportSeriesFilter
}

func (f *fakeImageReportRepo) LatencySeries(context.Context, ImageReportSeriesFilter) ([]ImageLatencyBucket, error) {
	return nil, nil
}
func (f *fakeImageReportRepo) StageLatencySeries(_ context.Context, filter ImageReportSeriesFilter) ([]ImageStageLatencyBucket, error) {
	f.lastFilter = filter
	return f.stageBuckets, nil
}
func (f *fakeImageReportRepo) RequestSeries(context.Context, ImageReportSeriesFilter) ([]ImageRequestBucket, error) {
	return f.reqBuckets, nil
}
func (f *fakeImageReportRepo) TodayBreakdown(context.Context, time.Time, time.Time) ([]ImageTodayItem, error) {
	return nil, nil
}
func (f *fakeImageReportRepo) ListImageAccountConcurrency(_ context.Context, category string) ([]ImageAccountConcurrency, error) {
	return f.categoryAccts[category], nil
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
	f := s.BuildSeriesFilter("GEMINI", "  m  ", nil, nil, "garbage", "Bad/Zone", now)
	require.Equal(t, OperationPlatformGemini, f.Platform)
	require.Equal(t, "m", f.Model)
	require.Equal(t, "1h", f.Bucket)
	require.Equal(t, "UTC", f.TZ)
	require.Equal(t, now, f.End)
	require.Equal(t, now.Add(-24*time.Hour), f.Start)
	require.Nil(t, f.GroupID)
	require.Nil(t, f.UserID)
}

func TestBuildSeriesFilterCarriesGroupAndUser(t *testing.T) {
	s := newOperationImageReportServiceForTest(&fakeImageReportRepo{}, &fakeConcurrency{})
	groupID, userID := int64(33), int64(244)
	f := s.BuildSeriesFilter("openai", "gpt-image-2", &groupID, &userID, "5m", "Asia/Shanghai", time.Now())
	require.NotNil(t, f.GroupID)
	require.Equal(t, groupID, *f.GroupID)
	require.NotNil(t, f.UserID)
	require.Equal(t, userID, *f.UserID)
	require.Equal(t, "5m", f.Bucket)
}

// 分段耗时曲线是定责用的：upstream 高说明上游/我们慢，response 高说明客户端
// 下载慢。这里确认筛选条件（尤其 UserID）原样透传到仓储，否则按用户检索会失效。
func TestStageLatencySeriesPassesFilterThrough(t *testing.T) {
	userID := int64(244)
	repo := &fakeImageReportRepo{stageBuckets: []ImageStageLatencyBucket{{Count: 7}}}
	s := newOperationImageReportServiceForTest(repo, &fakeConcurrency{})

	got, err := s.StageLatencySeries(context.Background(), ImageReportSeriesFilter{
		Platform: OperationPlatformOpenAI,
		UserID:   &userID,
		Bucket:   "1h",
	})
	require.NoError(t, err)
	require.Len(t, got, 1)
	require.Equal(t, int64(7), got[0].Count)
	require.NotNil(t, repo.lastFilter.UserID)
	require.Equal(t, userID, *repo.lastFilter.UserID)
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
		categoryAccts: map[string][]ImageAccountConcurrency{
			ImageAccountCategoryOpenAIOAuth: {{AccountID: 1, Concurrency: 3}, {AccountID: 2, Concurrency: 5}},
			ImageAccountCategoryAdobe:       {{AccountID: 4, Concurrency: 2}},
			ImageAccountCategoryGemini:      {{AccountID: 3, Concurrency: 4}},
		},
		alertAccts: []ImageAccountConcurrency{{AccountID: 1, Concurrency: 3}},
	}
	conc := &fakeConcurrency{values: map[int64]int{1: 2, 2: 1, 3: 0, 4: 1}}
	s := newOperationImageReportServiceForTest(repo, conc)
	ov, err := s.Concurrency(context.Background())
	require.NoError(t, err)
	require.Len(t, ov.Cards, 3)
	require.Equal(t, ImageAccountCategoryOpenAIOAuth, ov.Cards[0].Key)
	require.Equal(t, 8, ov.Cards[0].TotalConcurrency)
	require.Equal(t, 3, ov.Cards[0].CurrentConcurrency)
	require.True(t, ov.Cards[0].Available)
	require.Equal(t, ImageAccountCategoryAdobe, ov.Cards[1].Key)
	require.Equal(t, 2, ov.Cards[1].TotalConcurrency)
	require.Equal(t, 1, ov.Cards[1].CurrentConcurrency)
	require.Equal(t, ImageAccountCategoryGemini, ov.Cards[2].Key)
	require.Equal(t, 4, ov.Cards[2].TotalConcurrency)
	require.Equal(t, 0, ov.Cards[2].CurrentConcurrency)
	require.True(t, ov.Cards[2].Available)
	require.Equal(t, 1, ov.Alert.AccountCount)
	require.Equal(t, 3, ov.Alert.TotalConcurrency)
	require.Equal(t, 2, ov.Alert.CurrentConcurrency)
}

func TestConcurrencyDegradesWhenRedisFails(t *testing.T) {
	repo := &fakeImageReportRepo{
		categoryAccts: map[string][]ImageAccountConcurrency{
			ImageAccountCategoryOpenAIOAuth: {{AccountID: 1, Concurrency: 3}},
		},
	}
	s := newOperationImageReportServiceForTest(repo, &fakeConcurrency{err: errors.New("redis down")})
	ov, err := s.Concurrency(context.Background())
	require.NoError(t, err)
	require.False(t, ov.Cards[0].Available)
	require.Equal(t, 0, ov.Cards[0].CurrentConcurrency)
	require.Equal(t, 3, ov.Cards[0].TotalConcurrency)
}
