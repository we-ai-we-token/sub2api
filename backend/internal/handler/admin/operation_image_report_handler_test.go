package admin

import (
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

func TestOperationImageReportParseQueryInt64(t *testing.T) {
	gin.SetMode(gin.TestMode)
	h := &OperationImageReportHandler{}

	newCtx := func(target string) *gin.Context {
		c, _ := gin.CreateTestContext(httptest.NewRecorder())
		c.Request = httptest.NewRequest("GET", target, nil)
		return c
	}

	got := h.parseQueryInt64(newCtx("/?group_id=42"), "group_id")
	require.NotNil(t, got)
	require.Equal(t, int64(42), *got)

	// 用户检索用的 user_id 走同一个解析器
	gotUser := h.parseQueryInt64(newCtx("/?user_id=244"), "user_id")
	require.NotNil(t, gotUser)
	require.Equal(t, int64(244), *gotUser)

	// 非法值与缺省都按「不筛选」处理，不能退化成 0 把结果筛空
	require.Nil(t, h.parseQueryInt64(newCtx("/?group_id=abc"), "group_id"))
	require.Nil(t, h.parseQueryInt64(newCtx("/"), "group_id"))
	require.Nil(t, h.parseQueryInt64(newCtx("/?user_id="), "user_id"))
}
