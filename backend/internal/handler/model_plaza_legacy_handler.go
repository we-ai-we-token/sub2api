package handler

import (
	"sort"
	"strconv"
	"strings"

	"github.com/Wei-Shaw/sub2api/internal/pkg/response"
	"github.com/Wei-Shaw/sub2api/internal/server/middleware"
	"github.com/Wei-Shaw/sub2api/internal/service"

	"github.com/gin-gonic/gin"
)

// ModelPlazaLegacyHandler 处理用户侧「模型广场」查询。
//
// 模型广场是一个只读视图：用户先选一个自己可访问的分组，页面据此展示
//  1. 分组倍率（已含用户专属倍率覆盖）；
//  2. 该分组支持的模型数量；
//  3. 模型清单及其定价（价格已按分组有效倍率折算后返回，前端无需再乘）。
//
// 与「可用渠道」不同，模型广场以「分组」为中心聚合所有挂在该分组上的渠道的
// 支持模型，并对生图（按次）模型把渠道定价的 1K/2K/4K 档位与分组生图定价
// （image_price_1k/2k/4k）做回落，最终落成一行 1K/2K/4K 价格文字。
//
// 复用已有依赖（channelService + apiKeyService），不引入新的 service，
// 因此 wire 只需多挂一个 handler，便于独立维护与修改。
type ModelPlazaLegacyHandler struct {
	channelService *service.ChannelService
	apiKeyService  *service.APIKeyService
	gatewayService *service.GatewayService
}

// NewModelPlazaLegacyHandler 创建用户侧模型广场 handler。
//
// gatewayService 用于拿到「分组下账号实际可调用的具体模型名」（复用 /v1/models 的
// GetAvailableModels），把渠道里的通配符按次定价展开成具体模型行。
func NewModelPlazaLegacyHandler(
	channelService *service.ChannelService,
	apiKeyService *service.APIKeyService,
	gatewayService *service.GatewayService,
) *ModelPlazaLegacyHandler {
	return &ModelPlazaLegacyHandler{
		channelService: channelService,
		apiKeyService:  apiKeyService,
		gatewayService: gatewayService,
	}
}

// modelPlazaLegacyGroup 选择框 + 卡片所需的分组概要（白名单字段）。
//
// RateMultiplier 是分组默认倍率；UserRateMultiplier 是用户专属倍率（可能为空），
// 前端用 UserRateMultiplier ?? RateMultiplier 作为「有效倍率」展示。
type modelPlazaLegacyGroup struct {
	ID                 int64    `json:"id"`
	Name               string   `json:"name"`
	Platform           string   `json:"platform"`
	SubscriptionType   string   `json:"subscription_type"`
	IsExclusive        bool     `json:"is_exclusive"`
	RateMultiplier     float64  `json:"rate_multiplier"`
	UserRateMultiplier *float64 `json:"user_rate_multiplier"`
}

// modelPlazaLegacyImageTiers 生图（按次）模型的档位价格（已折算）。
//
// 分组按尺寸计费时填 Price1K/2K/4K；OpenAI 分组开启「质量计费」
// （ImageQualityBilling）时改填 PriceLow/Medium/High。两组互斥，未启用的一组为空。
// 任一档位可能为空（渠道与分组都未配置）。
type modelPlazaLegacyImageTiers struct {
	Price1K     *float64 `json:"price_1k"`
	Price2K     *float64 `json:"price_2k"`
	Price4K     *float64 `json:"price_4k"`
	PriceLow    *float64 `json:"price_low"`
	PriceMedium *float64 `json:"price_medium"`
	PriceHigh   *float64 `json:"price_high"`
}

// modelPlazaLegacyModel 模型清单中的一行（价格为「原价」，未折算）。
//
// 价格一律返回原价，倍率折算交给前端：前端用 token_multiplier / image_multiplier
// 展示「原价划线 + 折算后金额」的对比。这样前端能同时呈现原价与折后价。
//
// BillingMode == "token" 时取 Input/Output/CacheWrite/CacheRead/ImageOutput
// （单位为「每 token」，前端按需 ×1,000,000 展示为每百万 token）。
// BillingMode == "image"/"per_request" 时取 ImageTiers（1K/2K/4K）与 PerRequest
// （单价，单位为「每次」）。
type modelPlazaLegacyModel struct {
	Name             string                      `json:"name"`
	Platform         string                      `json:"platform"`
	BillingMode      string                      `json:"billing_mode"`
	InputPrice       *float64                    `json:"input_price"`
	OutputPrice      *float64                    `json:"output_price"`
	CacheWritePrice  *float64                    `json:"cache_write_price"`
	CacheReadPrice   *float64                    `json:"cache_read_price"`
	ImageOutputPrice *float64                    `json:"image_output_price"`
	PerRequestPrice  *float64                    `json:"per_request_price"`
	ImageTiers       *modelPlazaLegacyImageTiers `json:"image_tiers"`
}

// modelPlazaLegacyResponse 模型广场聚合响应。
//
// Models 里的价格是原价；TokenMultiplier / ImageMultiplier 是选中分组的有效倍率
// （已含用户专属倍率覆盖），前端据此把原价折算成实付价做对比展示。
type modelPlazaLegacyResponse struct {
	Groups          []modelPlazaLegacyGroup `json:"groups"`
	SelectedGroupID int64                   `json:"selected_group_id"`
	TokenMultiplier float64                 `json:"token_multiplier"`
	ImageMultiplier float64                 `json:"image_multiplier"`
	// ImageQualityBilling 为 true 时，选中分组按生图质量（low/medium/high）计费，
	// 生图行应展示 price_low/medium/high；否则按尺寸（1K/2K/4K）计费。
	ImageQualityBilling bool                    `json:"image_quality_billing"`
	Models              []modelPlazaLegacyModel `json:"models"`
}

// Models 返回模型广场数据：用户可选分组列表 + 选中分组的模型清单（含折算后定价）。
// GET /api/v1/model-plaza/models?group_id=123
//
// group_id 缺省或非法时回退到用户可访问分组里 id 最小的那个，与前端默认选中一致。
func (h *ModelPlazaLegacyHandler) Models(c *gin.Context) {
	subject, ok := middleware.GetAuthSubjectFromContext(c)
	if !ok {
		response.Unauthorized(c, "User not authenticated")
		return
	}
	ctx := c.Request.Context()

	groups, err := h.apiKeyService.GetAvailableGroups(ctx, subject.UserID)
	if err != nil {
		response.ErrorFrom(c, err)
		return
	}
	// 按 id 升序，前端选择框据此排列、默认选中第一个（id 最小）。
	sort.SliceStable(groups, func(i, j int) bool { return groups[i].ID < groups[j].ID })

	userRates, err := h.apiKeyService.GetUserGroupRates(ctx, subject.UserID)
	if err != nil {
		response.ErrorFrom(c, err)
		return
	}

	out := modelPlazaLegacyResponse{
		Groups:          make([]modelPlazaLegacyGroup, 0, len(groups)),
		TokenMultiplier: 1,
		ImageMultiplier: 1,
		Models:          make([]modelPlazaLegacyModel, 0),
	}
	for i := range groups {
		g := &groups[i]
		out.Groups = append(out.Groups, modelPlazaLegacyGroup{
			ID:                 g.ID,
			Name:               g.Name,
			Platform:           g.Platform,
			SubscriptionType:   g.SubscriptionType,
			IsExclusive:        g.IsExclusive,
			RateMultiplier:     g.RateMultiplier,
			UserRateMultiplier: lookupUserRateLegacy(userRates, g.ID),
		})
	}

	if len(groups) == 0 {
		response.Success(c, out)
		return
	}

	selected := resolveSelectedGroupLegacy(groups, c.Query("group_id"))
	out.SelectedGroupID = selected.ID

	tokenMult, imageMult := effectiveMultipliersLegacy(selected, userRates)
	out.TokenMultiplier = tokenMult
	out.ImageMultiplier = imageMult
	// OpenAI 分组开启质量计费时，生图行改按 low/medium/high 展示（与实际计费口径一致）。
	out.ImageQualityBilling = selected.ImageQualityBilling

	channels, err := h.channelService.ListAvailable(ctx)
	if err != nil {
		response.ErrorFrom(c, err)
		return
	}

	// 账号实际可调用的具体模型名（model_mapping keys），用于把渠道里的
	// 通配符按次定价（如 "gemini-3.1-flash-image_*"）展开成具体模型行。
	// 拿不到（无账号 mapping、用默认目录）时返回 nil，展开自动跳过。
	var accountModels []string
	if h.gatewayService != nil {
		accountModels = h.gatewayService.GetAvailableModels(ctx, &selected.ID, selected.Platform)
	}

	out.Models = buildModelPlazaLegacyModels(channels, selected, accountModels, h.channelService.DisplayPricingForModel)

	response.Success(c, out)
}

// resolveSelectedGroupLegacy 解析 group_id 查询参数；缺省或不在可访问列表内时回退到第一个
// （groups 已按 id 升序，所以是 id 最小的分组）。
func resolveSelectedGroupLegacy(groups []service.Group, raw string) *service.Group {
	if raw != "" {
		if id, err := strconv.ParseInt(raw, 10, 64); err == nil {
			for i := range groups {
				if groups[i].ID == id {
					return &groups[i]
				}
			}
		}
	}
	return &groups[0]
}

// lookupUserRateLegacy 返回用户在该分组的专属倍率指针；无专属配置返回 nil。
func lookupUserRateLegacy(rates map[int64]float64, groupID int64) *float64 {
	if rates == nil {
		return nil
	}
	if v, ok := rates[groupID]; ok {
		r := v
		return &r
	}
	return nil
}

// effectiveMultipliersLegacy 计算 token 与图片两条折算倍率。
//
//   - token 倍率：用户专属倍率覆盖分组默认倍率；
//   - 图片倍率：分组开启「图片独立倍率」时取 image_rate_multiplier，否则与 token 一致。
//
// 负数按 0 处理，避免反向折算出负价。与计费链路 billing_service 的 clamp 行为一致。
func effectiveMultipliersLegacy(g *service.Group, rates map[int64]float64) (tokenMult, imageMult float64) {
	tokenMult = g.RateMultiplier
	if r := lookupUserRateLegacy(rates, g.ID); r != nil {
		tokenMult = *r
	}
	imageMult = tokenMult
	if g.ImageRateIndependent {
		imageMult = g.ImageRateMultiplier
	}
	if tokenMult < 0 {
		tokenMult = 0
	}
	if imageMult < 0 {
		imageMult = 0
	}
	return tokenMult, imageMult
}

// buildModelPlazaLegacyModels 聚合挂在选中分组上的所有活跃渠道的支持模型，去重后取原价。
//
// 仅保留平台与选中分组一致的模型，防止跨平台信息泄漏（与「可用渠道」一致）。
// 去重以模型名（大小写不敏感）为键；遇到重复时优先保留带定价的条目。
// 倍率折算交给前端，这里只返回原价 + 分组生图定价回落。
//
// accountModels 是该分组下账号实际可调用的具体模型名（model_mapping keys）。
// SupportedModels 只含渠道里的「具体定价/映射模型」。这里用 accountModels 做两件事：
//  1. 通配符按次定价展开：对通配符前缀做匹配，补齐命中的具体模型（沿用通配符定价）；
//  2. 补齐「账号可调用但渠道未单独定价」的模型（如 gpt-image-2，计费走分组分辨率价）：
//     用 pricingFor（内置 LiteLLM 合成）拿展示定价；生图模型（LiteLLM image 模式，或
//     分组配了生图价且名字含 image）按 image 计费，由 resolveImageTierLegacy 回落分组分辨率价。
//
// pricingFor 为某模型返回内置展示定价（可为 nil）；测试可传 nil 或桩。
func buildModelPlazaLegacyModels(
	channels []service.AvailableChannel,
	group *service.Group,
	accountModels []string,
	pricingFor func(model string) *service.ChannelModelPricing,
) []modelPlazaLegacyModel {
	type entry struct {
		model   service.SupportedModel
		hasPric bool
	}
	byName := make(map[string]*entry)
	order := make([]string, 0)

	addModel := func(m service.SupportedModel) {
		key := strings.ToLower(m.Name)
		hasPric := modelHasPricingLegacy(m.Pricing)
		if existing, ok := byName[key]; ok {
			// 已有无定价、新条目有定价时替换，让用户尽量看到价格。
			if !existing.hasPric && hasPric {
				existing.model = m
				existing.hasPric = true
			}
			return
		}
		byName[key] = &entry{model: m, hasPric: hasPric}
		order = append(order, key)
	}

	for ci := range channels {
		ch := &channels[ci]
		if ch.Status != service.StatusActive {
			continue
		}
		if !channelHasGroupLegacy(ch.Groups, group.ID) {
			continue
		}
		for mi := range ch.SupportedModels {
			m := ch.SupportedModels[mi]
			if !strings.EqualFold(m.Platform, group.Platform) {
				continue
			}
			addModel(m)
		}
	}

	// 通配符按次定价展开：用账号实际模型补齐 SupportedModels 漏掉的具体模型。
	for _, m := range expandWildcardModelsLegacy(channels, group, accountModels) {
		addModel(m)
	}

	// 补齐账号可调用但渠道未单独定价的模型（用内置定价 / 分组生图价回落）。
	// 生图价按计费口径二选一：质量计费看 low/medium/high，否则看 1K/2K/4K。
	groupHasImagePrice := group.ImagePrice1K != nil || group.ImagePrice2K != nil || group.ImagePrice4K != nil
	if group.ImageQualityBilling {
		groupHasImagePrice = group.ImagePriceLow != nil || group.ImagePriceMedium != nil || group.ImagePriceHigh != nil
	}
	for _, name := range accountModels {
		if _, ok := byName[strings.ToLower(name)]; ok {
			continue // 已被渠道定价 / 通配符展开覆盖，保留其定价
		}
		var pricing *service.ChannelModelPricing
		if pricingFor != nil {
			pricing = pricingFor(name) // 内置 LiteLLM 合成（token / image），可能为 nil
		}
		// 生图模型纠正/兜底为 image 模式：让 resolveImageTierLegacy 回落到分组分辨率价。
		if !isImagePricingModeLegacy(pricing) && groupHasImagePrice && looksLikeImageModelLegacy(name) {
			pricing = &service.ChannelModelPricing{
				BillingMode: service.BillingModeImage,
				Platform:    group.Platform,
			}
		}
		addModel(service.SupportedModel{Name: name, Platform: group.Platform, Pricing: pricing})
	}

	models := make([]modelPlazaLegacyModel, 0, len(order))
	for _, key := range order {
		models = append(models, toModelPlazaLegacyModel(byName[key].model, group))
	}
	sort.SliceStable(models, func(i, j int) bool {
		if models[i].Platform != models[j].Platform {
			return models[i].Platform < models[j].Platform
		}
		return strings.ToLower(models[i].Name) < strings.ToLower(models[j].Name)
	})
	return models
}

// wildcardPricingRefLegacy 一条通配符定价模式：前缀（小写）+ 对应定价。
type wildcardPricingRefLegacy struct {
	prefixLower string
	pricing     *service.ChannelModelPricing
}

// expandWildcardModelsLegacy 用账号实际可调用的模型名，把选中分组下各活跃渠道里的
// 通配符定价模式（Models 含 "xxx*"）展开成具体模型。
//
// 仅匹配平台与分组一致的通配符定价；每个账号模型取首个命中的通配符定价。
// 只处理带定价的通配符（无定价的通配符没有可展示价格，跳过）。
func expandWildcardModelsLegacy(
	channels []service.AvailableChannel,
	group *service.Group,
	accountModels []string,
) []service.SupportedModel {
	if len(accountModels) == 0 {
		return nil
	}

	// 收集通配符定价模式（保持渠道/定价的稳定顺序，首个命中胜出）。
	refs := make([]wildcardPricingRefLegacy, 0)
	for ci := range channels {
		ch := &channels[ci]
		if ch.Status != service.StatusActive || !channelHasGroupLegacy(ch.Groups, group.ID) {
			continue
		}
		for pi := range ch.ModelPricing {
			p := &ch.ModelPricing[pi]
			if !strings.EqualFold(p.Platform, group.Platform) || !modelHasPricingLegacy(p) {
				continue
			}
			for _, pattern := range p.Models {
				if prefix, ok := strings.CutSuffix(pattern, "*"); ok {
					ref := p.Clone()
					refs = append(refs, wildcardPricingRefLegacy{
						prefixLower: strings.ToLower(prefix),
						pricing:     &ref,
					})
				}
			}
		}
	}
	if len(refs) == 0 {
		return nil
	}

	out := make([]service.SupportedModel, 0)
	for _, name := range accountModels {
		nameLower := strings.ToLower(name)
		for i := range refs {
			if strings.HasPrefix(nameLower, refs[i].prefixLower) {
				out = append(out, service.SupportedModel{
					Name:     name,
					Platform: group.Platform,
					Pricing:  refs[i].pricing,
				})
				break
			}
		}
	}
	return out
}

// channelHasGroupLegacy 判断渠道是否挂在指定分组上。
func channelHasGroupLegacy(groups []service.AvailableGroupRef, groupID int64) bool {
	for i := range groups {
		if groups[i].ID == groupID {
			return true
		}
	}
	return false
}

// isImagePricingModeLegacy 判断定价是否为生图/按次计费模式。
func isImagePricingModeLegacy(p *service.ChannelModelPricing) bool {
	return p != nil && (p.BillingMode == service.BillingModeImage || p.BillingMode == service.BillingModePerRequest)
}

// looksLikeImageModelLegacy 名字含 "image" 的启发式判断（覆盖 gpt-image-*、gemini-*-image 等），
// 仅在「内置定价缺失但分组配了生图价」时用于把模型纠正为 image 模式。
func looksLikeImageModelLegacy(name string) bool {
	return strings.Contains(strings.ToLower(name), "image")
}

// modelHasPricingLegacy 判断支持模型是否带有效定价（任一价格字段非空）。
func modelHasPricingLegacy(p *service.ChannelModelPricing) bool {
	if p == nil {
		return false
	}
	if p.InputPrice != nil || p.OutputPrice != nil || p.CacheWritePrice != nil ||
		p.CacheReadPrice != nil || p.ImageOutputPrice != nil || p.PerRequestPrice != nil {
		return true
	}
	for _, iv := range p.Intervals {
		if iv.PerRequestPrice != nil || iv.InputPrice != nil || iv.OutputPrice != nil {
			return true
		}
	}
	return false
}

// toModelPlazaLegacyModel 把支持模型转成模型广场 DTO（原价，未折算）。
// 生图按次模型的 1K/2K/4K 档位先取渠道定价区间，再回落分组生图定价。
func toModelPlazaLegacyModel(
	m service.SupportedModel,
	group *service.Group,
) modelPlazaLegacyModel {
	out := modelPlazaLegacyModel{
		Name:        m.Name,
		Platform:    m.Platform,
		BillingMode: string(service.BillingModeToken),
	}
	p := m.Pricing
	if p == nil {
		return out
	}
	if p.BillingMode != "" {
		out.BillingMode = string(p.BillingMode)
	}

	switch p.BillingMode {
	case service.BillingModeImage, service.BillingModePerRequest:
		out.PerRequestPrice = p.PerRequestPrice
		// 档位口径按「分组是否质量计费 + 该模型有无渠道价」逐模型决定：
		//  - 分组开质量计费且该模型无渠道价（如 gpt-image-2）→ 回落分组质量价，展示 low/medium/high；
		//  - 其余（该模型配了渠道价，或分组按尺寸计费）→ 展示 1K/2K/4K（渠道价/分组尺寸价）。
		// 两条分支复用同一套 resolveImageTierLegacy（渠道区间 → 渠道 flat 价 → 分组档价）回落逻辑，
		// 与真实计费口径保持一致；同组内不同模型可呈现不同档位类型。
		if group.ImageQualityBilling && !channelHasExplicitImagePriceLegacy(p) {
			out.ImageTiers = &modelPlazaLegacyImageTiers{
				PriceLow:    resolveImageTierLegacy(p, group, "low"),
				PriceMedium: resolveImageTierLegacy(p, group, "medium"),
				PriceHigh:   resolveImageTierLegacy(p, group, "high"),
			}
		} else {
			out.ImageTiers = &modelPlazaLegacyImageTiers{
				Price1K: resolveImageTierLegacy(p, group, "1K"),
				Price2K: resolveImageTierLegacy(p, group, "2K"),
				Price4K: resolveImageTierLegacy(p, group, "4K"),
			}
		}
	default:
		out.InputPrice = p.InputPrice
		out.OutputPrice = p.OutputPrice
		out.CacheWritePrice = p.CacheWritePrice
		out.CacheReadPrice = p.CacheReadPrice
		out.ImageOutputPrice = p.ImageOutputPrice
	}
	return out
}

// channelHasExplicitImagePriceLegacy 判断该模型的渠道定价是否显式配置了按次价
// （flat PerRequestPrice 或任一区间 PerRequestPrice）。
//
// 用于模型广场档位口径选择：分组开质量计费时，只有「渠道未配价、计费回落分组质量价」
// 的模型才展示 low/medium/high；渠道已配价的模型（如 gpt-image-2-low/-medium/-high）
// 计费走渠道价，仍展示 1K/2K/4K。
func channelHasExplicitImagePriceLegacy(p *service.ChannelModelPricing) bool {
	if p == nil {
		return false
	}
	if p.PerRequestPrice != nil {
		return true
	}
	for i := range p.Intervals {
		if p.Intervals[i].PerRequestPrice != nil {
			return true
		}
	}
	return false
}

// resolveImageTierLegacy 解析某档位（1K/2K/4K）的按次单价，优先级与真实计费
// （calculatePerRequestCost）保持一致：
//
//  1. 渠道定价区间里 TierLabel 匹配的 PerRequestPrice（GetRequestTierPrice）；
//  2. 渠道默认按次价 flat PerRequestPrice（DefaultPerRequestPrice）——这是关键：
//     渠道配了 flat 价（如 "gemini-3.1-flash-image_*" = $0.1）但没分档位时，
//     各尺寸都按 flat 价计费，不能跳过它直接用分组价；
//  3. 渠道完全没有按次价时，才回落分组生图定价（image_price_*）。
//
// 例：渠道 flat $0.1 + 分组生图价 0.12 → 真实计费 $0.1，这里也返回 $0.1。
func resolveImageTierLegacy(p *service.ChannelModelPricing, group *service.Group, tier string) *float64 {
	if p != nil {
		for i := range p.Intervals {
			iv := &p.Intervals[i]
			if iv.PerRequestPrice != nil && strings.EqualFold(strings.TrimSpace(iv.TierLabel), tier) {
				return iv.PerRequestPrice
			}
		}
		if p.PerRequestPrice != nil {
			return p.PerRequestPrice
		}
	}
	return group.GetImagePrice(tier)
}
