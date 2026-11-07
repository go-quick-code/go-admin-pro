// Package api 是 erp 模块的 HTTP 入口：路由、参数绑定与统一响应。
package api

import (
	"time"

	"github.com/gin-gonic/gin"
	"github.com/go-quick-code/go-admin-pro/internal/module/erp/service"
	"github.com/go-quick-code/go-admin-pro/internal/pkg/response"
	"github.com/go-quick-code/go-admin-pro/internal/pkg/web"
)

// Handler 是 erp 模块的 HTTP 入口。
// requirePermission 复用 system 模块的权限中间件（与 crm/oa 同款装配方式），
// 权限码与菜单种子保持一致（erp:<entity>:*）。
// location 是应用业务时区（ADR 0006），ERP Excel 导出的文件名日期键统一使用它。
type Handler struct {
	svc               *service.Service
	requirePermission func(string) gin.HandlerFunc
	location          *time.Location
}

// New 创建 erp Handler。location 为 nil 时回退 time.Local
// （生产装配固定传 config.Location()）。
func New(svc *service.Service, requirePermission func(string) gin.HandlerFunc, location *time.Location) *Handler {
	if location == nil {
		location = time.Local
	}
	return &Handler{svc: svc, requirePermission: requirePermission, location: location}
}

// RegisterProtected 注册需要登录的 ERP 接口，rg 上应已挂载 Auth 中间件。
// 统一前缀 /erp（docs/erp-mvp-spec.md §5：/admin-api/erp）。
func (h *Handler) RegisterProtected(rg *gin.RouterGroup) {
	h.RegisterProductCategoryRoutes(rg)
	h.RegisterProductUnitRoutes(rg)
	h.RegisterProductRoutes(rg)
	// 仓库/客户/供应商主数据（issue #89，spec §3.1/§5）。
	h.RegisterWarehouseRoutes(rg)
	h.RegisterCustomerRoutes(rg)
	h.RegisterSupplierRoutes(rg)
	// 库存核心与其它入库/出库单（issue #90，spec §3.2/§3.5/§5）。
	h.RegisterStockRoutes(rg)
	h.RegisterStockRecordRoutes(rg)
	h.RegisterStockInRoutes(rg)
	h.RegisterStockOutRoutes(rg)
	// 采购三单（issue #91，spec §3.3/§4.4/§5）：订单/入库/退货。
	h.RegisterPurchaseOrderRoutes(rg)
	h.RegisterPurchaseInRoutes(rg)
	h.RegisterPurchaseReturnRoutes(rg)
}

// ---------- 通用绑定助手（与 system/crm 模块同构，保持模块自包含）----------

func bindJSON(c *gin.Context, v interface{}) bool {
	if err := c.ShouldBindJSON(v); err != nil {
		response.BadRequest(c, "请求参数错误："+err.Error())
		return false
	}
	return true
}

func bindQuery(c *gin.Context, v interface{}) bool {
	if err := c.ShouldBindQuery(v); err != nil {
		response.BadRequest(c, "查询参数错误："+err.Error())
		return false
	}
	return true
}

func pathID(c *gin.Context) (int64, bool) {
	id, ok := web.ParseID(c, "id")
	if !ok {
		response.BadRequest(c, "非法的 ID")
		return 0, false
	}
	return id, true
}
