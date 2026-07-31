//go:build unit

package service

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

// setupHysteresisService returns a service wired for balance-low notifications with
// global enabled and a fixed threshold of 10.
func setupHysteresisService(t *testing.T) (*BalanceNotifyService, *mockSettingRepo, *User) {
	t.Helper()
	s, repo := newBalanceNotifyServiceForTest()
	repo.data[SettingKeyBalanceLowNotifyEnabled] = "true"
	repo.data[SettingKeyBalanceLowNotifyThreshold] = "10"
	// Empty Email keeps the async dispatch a no-op (no recipients), so these tests
	// observe only the synchronous hysteresis state transitions in the settings repo.
	u := &User{ID: 42, BalanceNotifyEnabled: true}
	return s, repo, u
}

func alertedValue(repo *mockSettingRepo, userID int64) (string, bool) {
	repo.mu.Lock()
	defer repo.mu.Unlock()
	v, ok := repo.data[balanceLowAlertedKey(userID)]
	return v, ok
}

// TestBalanceHysteresis_RechargeThenDrop_ReAlerts reproduces the customer report:
// alert in the morning, recharge at noon, drop below threshold again in the afternoon
// must notify a second time (the old calendar-day dedupe wrongly suppressed it).
func TestBalanceHysteresis_RechargeThenDrop_ReAlerts(t *testing.T) {
	s, repo, u := setupHysteresisService(t)
	ctx := context.Background()

	// Morning: 20 -> 5 crosses below 10 -> alert fires, flag set.
	s.CheckBalanceAfterDeduction(ctx, u, 20, 15)
	first, ok := alertedValue(repo, u.ID)
	require.True(t, ok, "first crossing should set the alerted flag")
	require.NotEmpty(t, first)

	// Noon recharge: a subsequent deduction now starts above the threshold and stays
	// above (100 -> 95). This must re-arm (clear) the alert.
	s.CheckBalanceAfterDeduction(ctx, u, 100, 5)
	_, ok = alertedValue(repo, u.ID)
	require.False(t, ok, "healthy deduction after recharge should re-arm (clear) the flag")

	// Afternoon: 20 -> 5 crosses again -> must re-alert (flag set again).
	s.CheckBalanceAfterDeduction(ctx, u, 20, 15)
	second, ok := alertedValue(repo, u.ID)
	require.True(t, ok, "second crossing after re-arm should alert again")
	require.NotEmpty(t, second)
}

// TestBalanceHysteresis_RechargeThenBigDrop_ReAlerts covers the recharge case where the
// very next deduction is itself a crossing (recharge to 100, then a single 95 spend).
// A pure once-per-cycle flag would wrongly suppress this; the cooldown lets it through
// because the prior alert is well past the cooldown window.
func TestBalanceHysteresis_RechargeThenBigDrop_ReAlerts(t *testing.T) {
	s, repo, u := setupHysteresisService(t)
	ctx := context.Background()

	// Prior cycle alerted long ago (well past the cooldown).
	old := time.Now().UTC().Add(-2 * time.Hour).Format(time.RFC3339Nano)
	repo.data[balanceLowAlertedKey(u.ID)] = old

	// Recharge to 100, then spend 95 in one shot: 100 -> 5 crosses below 10.
	s.CheckBalanceAfterDeduction(ctx, u, 100, 95)
	cur, ok := alertedValue(repo, u.ID)
	require.True(t, ok)
	require.NotEqual(t, old, cur, "crossing past the cooldown must re-alert (flag timestamp advances)")
}

// TestBalanceHysteresis_DuplicateCrossingWithinCooldown_Suppressed guards the legacy
// snapshot / concurrent-deduction path: the same crossing observed again within the
// cooldown must not re-send.
func TestBalanceHysteresis_DuplicateCrossingWithinCooldown_Suppressed(t *testing.T) {
	s, repo, u := setupHysteresisService(t)
	ctx := context.Background()

	// A very recent prior alert (this cycle).
	recent := time.Now().UTC().Add(-1 * time.Minute).Format(time.RFC3339Nano)
	repo.data[balanceLowAlertedKey(u.ID)] = recent

	// Same crossing replayed: 20 -> 5. Within cooldown -> suppressed, flag unchanged.
	s.CheckBalanceAfterDeduction(ctx, u, 20, 15)
	cur, ok := alertedValue(repo, u.ID)
	require.True(t, ok)
	require.Equal(t, recent, cur, "duplicate crossing within cooldown must not re-alert (flag unchanged)")
}

// TestBalanceHysteresis_FirstCrossingNoPriorState alerts on a clean first crossing.
func TestBalanceHysteresis_FirstCrossingNoPriorState(t *testing.T) {
	s, repo, u := setupHysteresisService(t)
	ctx := context.Background()

	s.CheckBalanceAfterDeduction(ctx, u, 20, 15)
	v, ok := alertedValue(repo, u.ID)
	require.True(t, ok, "first-ever crossing should alert")
	require.NotEmpty(t, v)
}

// TestBalanceHysteresis_ContinuedSpendingBelowThreshold_NoReAlert verifies that once
// below the threshold, continued spending (already-below, no new crossing) neither
// re-alerts nor re-arms.
func TestBalanceHysteresis_ContinuedSpendingBelowThreshold_NoReAlert(t *testing.T) {
	s, repo, u := setupHysteresisService(t)
	ctx := context.Background()

	// First crossing sets the flag.
	s.CheckBalanceAfterDeduction(ctx, u, 20, 15)
	first, ok := alertedValue(repo, u.ID)
	require.True(t, ok)

	// Already below threshold: 5 -> 2. No crossing, oldBalance < threshold so no re-arm.
	s.CheckBalanceAfterDeduction(ctx, u, 5, 3)
	cur, ok := alertedValue(repo, u.ID)
	require.True(t, ok, "continued below-threshold spending must not clear the flag")
	require.Equal(t, first, cur, "flag must remain unchanged while still in the low cycle")
}
