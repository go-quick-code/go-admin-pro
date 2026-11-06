package api

import "github.com/gin-gonic/gin"

// RegisterProtected 注册 message 模块接口（调用方需已挂载 Auth 中间件）。
// 权限码见 docs/platform-mvp-spec.md §5.2；渠道/模板的「仅平台租户」在
// service 层校验（非平台租户 403），这里只做权限码门槛。
func (h *Handler) RegisterProtected(rg *gin.RouterGroup) {
	channel := rg.Group("/message/channel")
	channel.GET("/page", h.perm("message:channel:query"), h.PageChannels)
	channel.POST("", h.perm("message:channel:create"), h.CreateChannel)
	channel.PUT("", h.perm("message:channel:update"), h.UpdateChannel)
	channel.PUT("/:id/status", h.perm("message:channel:update"), h.UpdateChannelStatus)
	channel.DELETE("/:id", h.perm("message:channel:delete"), h.DeleteChannel)

	template := rg.Group("/message/template")
	template.GET("/page", h.perm("message:template:query"), h.PageTemplates)
	template.POST("", h.perm("message:template:create"), h.CreateTemplate)
	template.PUT("", h.perm("message:template:update"), h.UpdateTemplate)
	template.DELETE("/:id", h.perm("message:template:delete"), h.DeleteTemplate)

	// 发送：短信/邮件共用 SendReq，渠道类型由端点决定。
	rg.POST("/message/sms/send", h.perm("message:sms:send"), h.SendSMS)
	rg.POST("/message/mail/send", h.perm("message:mail:send"), h.SendMail)

	log := rg.Group("/message/log")
	log.GET("/page", h.perm("message:log:query"), h.PageLogs)
	log.POST("/:id/resend", h.perm("message:log:resend"), h.ResendLog)
	// 静态路由先于 /:id 注册，避免 "clear" 被当成日志 ID。
	log.DELETE("/clear", h.perm("message:log:delete"), h.ClearLogs)
	log.DELETE("/:id", h.perm("message:log:delete"), h.DeleteLog)
}
