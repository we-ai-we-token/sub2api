package service

import "context"

type channelImagePricingInput struct {
	Ctx            context.Context
	BillingService *BillingService
	Resolver       *ModelPricingResolver
	BillingModel   string
	APIKey         *APIKey
	Tokens         UsageTokens
	ImageCount     int
	SizeTier       string
	Multiplier     float64
}

func tryCalculateChannelImageCost(input channelImagePricingInput) (*CostBreakdown, bool) {
	if input.BillingService == nil || input.Resolver == nil ||
		input.APIKey == nil || input.APIKey.Group == nil ||
		input.BillingModel == "" || input.ImageCount <= 0 {
		return nil, false
	}

	groupID := input.APIKey.Group.ID
	resolved := input.Resolver.Resolve(input.Ctx, PricingInput{
		Model:   input.BillingModel,
		GroupID: &groupID,
	})
	if resolved == nil || resolved.Source != PricingSourceChannel {
		return nil, false
	}

	mode := resolved.Mode
	if mode == "" {
		mode = BillingModeToken
	}
	switch mode {
	case BillingModeToken:
		if input.Tokens.ImageOutputTokens <= 0 || !resolved.HasChannelImageOutputPrice {
			return nil, false
		}
	case BillingModePerRequest, BillingModeImage:
	default:
		return nil, false
	}

	cost, err := input.BillingService.CalculateCostUnified(CostInput{
		Ctx:            input.Ctx,
		Model:          input.BillingModel,
		GroupID:        &groupID,
		Tokens:         input.Tokens,
		RequestCount:   input.ImageCount,
		SizeTier:       input.SizeTier,
		RateMultiplier: input.Multiplier,
		Resolver:       input.Resolver,
		Resolved:       resolved,
	})
	if err != nil {
		return nil, false
	}
	return cost, true
}
