// router.go 注册 bpm 路由与权限码（spec #42 §5 bpm 节）。
// 定义管理：发布与停用共用 bpm:definition:publish（同一人事动作权限）。
// 运行时（issue #46）：查询挂 bpm:task:query / bpm:instance:query；审批动作
// 以「当前办理人/发起人」身份收口（service 层判定，spec §5 不设权限码门槛）。
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

// RegisterRuntimeRoutes 注册运行时路由（实例/任务，issue #46）。
func (h *Handler) RegisterRuntimeRoutes(rg *gin.RouterGroup) {
	task := rg.Group("/bpm/task")
	task.GET("/page", h.requirePermission("bpm:task:query"), h.PageMyTasks)
	task.POST("/:id/approve", h.ApproveTask)
	task.POST("/:id/reject", h.RejectTask)
	task.POST("/:id/transfer", h.TransferTask)

	instance := rg.Group("/bpm/instance")
	instance.GET("/page", h.requirePermission("bpm:instance:query"), h.PageInstances)
	instance.GET("/:id", h.GetInstanceDetail)
	instance.POST("/:id/withdraw", h.WithdrawInstance)
}
