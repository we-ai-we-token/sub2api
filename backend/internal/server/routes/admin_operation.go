package routes

import (
	"github.com/Wei-Shaw/sub2api/internal/handler"

	"github.com/gin-gonic/gin"
)

// registerAdminOperationRoutes 注册「运营管理」相关只读路由。
func registerAdminOperationRoutes(admin *gin.RouterGroup, h *handler.Handlers) {
	op := admin.Group("/operation/image-report")
	{
		op.GET("/overview", h.Admin.OperationImageReport.Overview)
		op.GET("/concurrency", h.Admin.OperationImageReport.Concurrency)
		op.GET("/latency-series", h.Admin.OperationImageReport.LatencySeries)
		op.GET("/stage-latency-series", h.Admin.OperationImageReport.StageLatencySeries)
		op.GET("/request-series", h.Admin.OperationImageReport.RequestSeries)
		op.GET("/filters", h.Admin.OperationImageReport.Filters)
	}
	records := admin.Group("/operation/image-records")
	{
		records.GET("", h.Admin.ImageGenerationRecord.List)
	}
}
