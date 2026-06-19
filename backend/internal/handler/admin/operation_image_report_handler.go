package admin

import (
	"strconv"
	"strings"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/pkg/response"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
)

type OperationImageReportHandler struct {
	svc *service.OperationImageReportService
}

func NewOperationImageReportHandler(svc *service.OperationImageReportService) *OperationImageReportHandler {
	return &OperationImageReportHandler{svc: svc}
}

func (h *OperationImageReportHandler) parseGroupID(c *gin.Context) *int64 {
	raw := strings.TrimSpace(c.Query("group_id"))
	if raw == "" {
		return nil
	}
	id, err := strconv.ParseInt(raw, 10, 64)
	if err != nil {
		return nil
	}
	return &id
}

func (h *OperationImageReportHandler) seriesFilter(c *gin.Context) service.ImageReportSeriesFilter {
	return h.svc.BuildSeriesFilter(c.Query("platform"), c.Query("model"), h.parseGroupID(c), c.Query("bucket"), c.Query("tz"), time.Now())
}

// GET /admin/operation/image-report/overview
func (h *OperationImageReportHandler) Overview(c *gin.Context) {
	data, err := h.svc.TodayBreakdown(c.Request.Context(), c.Query("tz"), time.Now())
	if err != nil {
		response.ErrorFrom(c, err)
		return
	}
	response.Success(c, gin.H{"today": data})
}

// GET /admin/operation/image-report/concurrency
func (h *OperationImageReportHandler) Concurrency(c *gin.Context) {
	data, err := h.svc.Concurrency(c.Request.Context())
	if err != nil {
		response.ErrorFrom(c, err)
		return
	}
	response.Success(c, data)
}

// GET /admin/operation/image-report/latency-series
func (h *OperationImageReportHandler) LatencySeries(c *gin.Context) {
	data, err := h.svc.LatencySeries(c.Request.Context(), h.seriesFilter(c))
	if err != nil {
		response.ErrorFrom(c, err)
		return
	}
	response.Success(c, gin.H{"buckets": data})
}

// GET /admin/operation/image-report/request-series
func (h *OperationImageReportHandler) RequestSeries(c *gin.Context) {
	data, err := h.svc.RequestSeries(c.Request.Context(), h.seriesFilter(c))
	if err != nil {
		response.ErrorFrom(c, err)
		return
	}
	response.Success(c, gin.H{"buckets": data})
}

// GET /admin/operation/image-report/filters
func (h *OperationImageReportHandler) Filters(c *gin.Context) {
	data, err := h.svc.FilterOptions(c.Request.Context(), c.Query("platform"))
	if err != nil {
		response.ErrorFrom(c, err)
		return
	}
	response.Success(c, data)
}
