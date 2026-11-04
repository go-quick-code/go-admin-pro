package response

import (
	"log/slog"
	"net/http"

	"github.com/gin-gonic/gin"
	"github.com/lautung/go-admin-pro/internal/pkg/xerr"
)

// Body 是统一响应体，与芋道的 { code, data, msg } 保持一致。
type Body struct {
	Code int         `json:"code"`
	Data interface{} `json:"data"`
	Msg  string      `json:"msg"`
}

// PageResult 是分页响应。
type PageResult struct {
	List  interface{} `json:"list"`
	Total int64       `json:"total"`
}

func OK(c *gin.Context, data interface{}) {
	c.Set("bizCode", 0)
	c.JSON(http.StatusOK, Body{Code: 0, Data: data, Msg: ""})
}

func Page(c *gin.Context, list interface{}, total int64) {
	OK(c, PageResult{List: list, Total: total})
}

func BadRequest(c *gin.Context, msg string) {
	Fail(c, xerr.BadRequest(msg))
}

// Fail 根据错误类型输出响应。
func Fail(c *gin.Context, err error) {
	if e, ok := xerr.As(err); ok {
		c.Set("bizCode", e.Code)
		c.Set("bizMsg", e.Msg)
		if e.Status == http.StatusOK {
			c.JSON(http.StatusOK, Body{Code: e.Code, Data: nil, Msg: e.Msg})
			return
		}
		c.JSON(e.Status, Body{Code: e.Code, Data: nil, Msg: e.Msg})
		return
	}
	c.Set("bizCode", 500)
	c.Set("bizMsg", "服务器内部错误")
	slog.Error("unhandled error", "path", c.FullPath(), "err", err)
	c.JSON(http.StatusInternalServerError, Body{Code: 500, Data: nil, Msg: "服务器内部错误"})
}

// Abort 输出错误并中断中间件链。
func Abort(c *gin.Context, err error) {
	Fail(c, err)
	c.Abort()
}
