// router.go 注册 bpm 定义管理的路由与权限码（spec #42 §5 bpm 节）。
// 发布与停用共用 bpm:definition:publish（发布/停用是同一人事动作权限）。
package api

import "github.com/gin-gonic/gin"

// RegisterDefinitionRoutes 注册 /bpm/definition 路由。
func (h *Handler) RegisterDefinitionRoutes(rg *gin.RouterGroup) {
	definition := rg.Group("/bpm/definition")
	definition.GET("/page", h.requirePermission("bpm:definition:query"), h.PageDefinitions)
	definition.GET("/:id", h.requirePermission("bpm:definition:query"), h.GetDefinition)
	definition.POST("", h.requirePermission("bpm:definition:create"), h.CreateDefinition)
	definition.PUT("", h.requirePermission("bpm:definition:update"), h.UpdateDefinition)
	definition.DELETE("/:id", h.requirePermission("bpm:definition:delete"), h.DeleteDefinition)
	definition.POST("/:id/publish", h.requirePermission("bpm:definition:publish"), h.PublishDefinition)
	definition.POST("/:id/disable", h.requirePermission("bpm:definition:publish"), h.DisableDefinition)
}
