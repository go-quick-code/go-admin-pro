package web

import (
	"strconv"

	"github.com/gin-gonic/gin"
)

// PageQuery 是通用分页参数。
type PageQuery struct {
	PageNo   int `form:"pageNo" json:"pageNo"`
	PageSize int `form:"pageSize" json:"pageSize"`
}

// Normalize 修正非法分页参数。
func (p *PageQuery) Normalize() {
	if p.PageNo <= 0 {
		p.PageNo = 1
	}
	if p.PageSize <= 0 {
		p.PageSize = 10
	}
	if p.PageSize > 200 {
		p.PageSize = 200
	}
}

func (p PageQuery) Offset() int { return (p.PageNo - 1) * p.PageSize }

// BindPage 解析分页参数。
func BindPage(c *gin.Context) PageQuery {
	var p PageQuery
	p.PageNo, _ = strconv.Atoi(c.DefaultQuery("pageNo", "1"))
	p.PageSize, _ = strconv.Atoi(c.DefaultQuery("pageSize", "10"))
	p.Normalize()
	return p
}

// ParseID 解析路径参数中的 ID。
func ParseID(c *gin.Context, name string) (int64, bool) {
	id, err := strconv.ParseInt(c.Param(name), 10, 64)
	if err != nil || id <= 0 {
		return 0, false
	}
	return id, true
}
