package admin

import (
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

func TestOperationImageReportParseGroupID(t *testing.T) {
	gin.SetMode(gin.TestMode)
	h := &OperationImageReportHandler{}

	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	c.Request = httptest.NewRequest("GET", "/?group_id=42", nil)
	got := h.parseGroupID(c)
	require.NotNil(t, got)
	require.Equal(t, int64(42), *got)

	c2, _ := gin.CreateTestContext(httptest.NewRecorder())
	c2.Request = httptest.NewRequest("GET", "/?group_id=abc", nil)
	require.Nil(t, h.parseGroupID(c2))

	c3, _ := gin.CreateTestContext(httptest.NewRecorder())
	c3.Request = httptest.NewRequest("GET", "/", nil)
	require.Nil(t, h.parseGroupID(c3))
}
