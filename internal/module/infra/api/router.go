package api

import "github.com/gin-gonic/gin"

// RegisterProtected 注册 infra 模块接口（调用方需已挂载 Auth 中间件）。
func (h *Handler) RegisterProtected(rg *gin.RouterGroup) {
	file := rg.Group("/infra/file")
	file.GET("/page", h.perm("infra:file:query"), h.PageFiles)
	// 鉴权下载（PR #39 残余风险 A）：公开 /files 静态目录已关闭，文件只能经此读取。
	file.GET("/:id/download", h.perm("infra:file:query"), h.DownloadFile)
	file.POST("/upload", h.perm("infra:file:upload"), h.UploadFile)
	file.DELETE("/:id", h.perm("infra:file:delete"), h.DeleteFile)

	config := rg.Group("/infra/config")
	config.GET("/page", h.perm("infra:config:query"), h.PageConfigs)
	config.GET("/:id", h.perm("infra:config:query"), h.GetConfig)
	config.POST("", h.perm("infra:config:create"), h.CreateConfig)
	config.PUT("", h.perm("infra:config:update"), h.UpdateConfig)
	config.DELETE("/:id", h.perm("infra:config:delete"), h.DeleteConfig)

	job := rg.Group("/infra/job")
	job.GET("/page", h.perm("infra:job:query"), h.PageJobs)
	job.GET("/handlers", h.perm("infra:job:query"), h.ListJobHandlers)
	job.GET("/:id", h.perm("infra:job:query"), h.GetJob)
	job.POST("", h.perm("infra:job:create"), h.CreateJob)
	job.PUT("", h.perm("infra:job:update"), h.UpdateJob)
	job.PUT("/status", h.perm("infra:job:update"), h.UpdateJobStatus)
	job.PUT("/:id/run", h.perm("infra:job:update"), h.RunJob)
	job.DELETE("/:id", h.perm("infra:job:delete"), h.DeleteJob)
	job.GET("/log/page", h.perm("infra:job:query"), h.PageJobLogs)

	// 数据库文档（issue #83，spec §4.9/§5.8）：仅平台租户（service 层收口 403）。
	dbdoc := rg.Group("/infra/db-doc")
	dbdoc.GET("/tables", h.perm("infra:db-doc:query"), h.ListDBTables)
	dbdoc.GET("/table/:name", h.perm("infra:db-doc:query"), h.GetDBTable)
	dbdoc.GET("/export", h.perm("infra:db-doc:query"), h.ExportDBDoc)
}
