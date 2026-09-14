package service

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"
)

// The images profile is inert unless HTTPUpstreamProfileFromContext allowlists
// it: an unlisted profile is silently downgraded to Default, with no error and
// no log, and every image request would quietly land in the unprofiled pool.
func TestHTTPUpstreamProfileOpenAIImagesSurvivesContextRoundTrip(t *testing.T) {
	ctx := WithHTTPUpstreamProfile(context.Background(), HTTPUpstreamProfileOpenAIImages)
	require.Equal(t, HTTPUpstreamProfileOpenAIImages, HTTPUpstreamProfileFromContext(ctx))
}

// The OAuth image path re-wraps a context that the shared Responses builder has
// already tagged with the text profile. Guard the "later write wins" assumption.
func TestHTTPUpstreamProfileImagesOverridesOpenAI(t *testing.T) {
	ctx := WithHTTPUpstreamProfile(context.Background(), HTTPUpstreamProfileOpenAI)
	ctx = WithHTTPUpstreamProfile(ctx, HTTPUpstreamProfileOpenAIImages)
	require.Equal(t, HTTPUpstreamProfileOpenAIImages, HTTPUpstreamProfileFromContext(ctx))
}

// 挂哪个 profile 由系统设置决定；settingService 为 nil 时必须回落到改动前的行为，
// 因为大量网关单测构造的 service 没有 settingService，直接解引用会 panic。
func TestOpenAIImagesUpstreamProfileFallsBackWithoutSettingService(t *testing.T) {
	var gw *OpenAIGatewayService
	require.Equal(t, HTTPUpstreamProfileOpenAI, gw.openAIImagesUpstreamProfile(context.Background()))

	gw = &OpenAIGatewayService{}
	require.Equal(t, HTTPUpstreamProfileOpenAI, gw.openAIImagesUpstreamProfile(context.Background()))

	var probe *AccountTestService
	require.Equal(t, HTTPUpstreamProfileOpenAI, probe.openAIImagesUpstreamProfile(context.Background()))

	probe = &AccountTestService{}
	require.Equal(t, HTTPUpstreamProfileOpenAI, probe.openAIImagesUpstreamProfile(context.Background()))
}
