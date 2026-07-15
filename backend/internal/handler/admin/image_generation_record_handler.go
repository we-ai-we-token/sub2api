package admin

import (
	"strconv"
	"strings"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/pkg/response"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
)

// ImageGenerationRecordHandler 提供生图记录（image_generation_records）的管理端查询。
type ImageGenerationRecordHandler struct {
	svc *service.ImageGenerationRecordService
}

func NewImageGenerationRecordHandler(svc *service.ImageGenerationRecordService) *ImageGenerationRecordHandler {
	return &ImageGenerationRecordHandler{svc: svc}
}

// List 分页查询生图记录。
// GET /admin/operation/image-records
// 参数：page、page_size、start_time/end_time（RFC3339，缺省最近 24h）、
// platform、model（前缀匹配）、success（true/false）、user_id、account_id、group_id、min_total_ms。
func (h *ImageGenerationRecordHandler) List(c *gin.Context) {
	page, pageSize := response.ParsePagination(c)

	filter := service.ImageGenerationRecordListFilter{
		Platform: c.Query("platform"),
		Model:    c.Query("model"),
		Page:     page,
		PageSize: pageSize,
	}

	if t, ok := parseImageRecordTime(c.Query("start_time")); ok {
		filter.StartTime = t
	}
	if t, ok := parseImageRecordTime(c.Query("end_time")); ok {
		filter.EndTime = t
	}
	if raw := strings.TrimSpace(c.Query("success")); raw != "" {
		if v, err := strconv.ParseBool(raw); err == nil {
			filter.Success = &v
		}
	}
	filter.UserID = parseImageRecordInt64(c.Query("user_id"))
	filter.AccountID = parseImageRecordInt64(c.Query("account_id"))
	filter.GroupID = parseImageRecordInt64(c.Query("group_id"))
	filter.MinTotalMs = parseImageRecordInt64(c.Query("min_total_ms"))

	items, total, err := h.svc.List(c.Request.Context(), filter)
	if err != nil {
		response.ErrorFrom(c, err)
		return
	}
	response.Paginated(c, items, total, page, pageSize)
}

func parseImageRecordTime(raw string) (time.Time, bool) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return time.Time{}, false
	}
	if t, err := time.Parse(time.RFC3339, raw); err == nil {
		return t, true
	}
	// 兼容毫秒时间戳
	if ms, err := strconv.ParseInt(raw, 10, 64); err == nil && ms > 0 {
		return time.UnixMilli(ms), true
	}
	return time.Time{}, false
}

func parseImageRecordInt64(raw string) *int64 {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return nil
	}
	v, err := strconv.ParseInt(raw, 10, 64)
	if err != nil {
		return nil
	}
	return &v
}
