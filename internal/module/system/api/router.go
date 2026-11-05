package api

import "github.com/gin-gonic/gin"

// RegisterPublic 注册无需登录的接口。
func (h *Handler) RegisterPublic(rg *gin.RouterGroup) {
	auth := rg.Group("/system/auth")
	auth.POST("/login", h.Login)
	auth.GET("/captcha", h.Captcha)
}

// RegisterProtected 注册需要登录的接口，rg 上应已挂载 Auth 中间件。
func (h *Handler) RegisterProtected(rg *gin.RouterGroup) {
	// 登录态相关
	auth := rg.Group("/system/auth")
	auth.POST("/logout", h.Logout)
	auth.GET("/user-info", h.UserInfo)
	auth.PUT("/profile", h.UpdateProfile)
	auth.PUT("/password", h.ChangePassword)

	// 仪表盘
	rg.GET("/system/dashboard/summary", h.DashboardSummary)

	// 用户
	user := rg.Group("/system/user")
	user.GET("/page", h.RequirePermission("system:user:query"), h.PageUsers)
	user.GET("/simple-list", h.ListSimpleUsers)
	user.GET("/:id", h.RequirePermission("system:user:query"), h.GetUser)
	user.POST("", h.RequirePermission("system:user:create"), h.CreateUser)
	user.PUT("", h.RequirePermission("system:user:update"), h.UpdateUser)
	user.PUT("/status", h.RequirePermission("system:user:update"), h.UpdateUserStatus)
	user.PUT("/password", h.RequirePermission("system:user:update"), h.ResetUserPassword)
	user.DELETE("/:id", h.RequirePermission("system:user:delete"), h.DeleteUser)

	// 角色
	role := rg.Group("/system/role")
	role.GET("/page", h.RequirePermission("system:role:query"), h.PageRoles)
	role.GET("/simple-list", h.ListSimpleRoles)
	role.GET("/:id", h.RequirePermission("system:role:query"), h.GetRole)
	role.POST("", h.RequirePermission("system:role:create"), h.CreateRole)
	role.PUT("", h.RequirePermission("system:role:update"), h.UpdateRole)
	role.DELETE("/:id", h.RequirePermission("system:role:delete"), h.DeleteRole)

	// 菜单
	menu := rg.Group("/system/menu")
	menu.GET("/list", h.RequirePermission("system:menu:query"), h.ListMenus)
	menu.GET("/:id", h.RequirePermission("system:menu:query"), h.GetMenu)
	menu.POST("", h.RequirePermission("system:menu:create"), h.CreateMenu)
	menu.PUT("", h.RequirePermission("system:menu:update"), h.UpdateMenu)
	menu.DELETE("/:id", h.RequirePermission("system:menu:delete"), h.DeleteMenu)

	// 部门
	dept := rg.Group("/system/dept")
	dept.GET("/tree", h.TreeDepts)
	dept.GET("/:id", h.RequirePermission("system:dept:query"), h.GetDept)
	dept.POST("", h.RequirePermission("system:dept:create"), h.CreateDept)
	dept.PUT("", h.RequirePermission("system:dept:update"), h.UpdateDept)
	dept.DELETE("/:id", h.RequirePermission("system:dept:delete"), h.DeleteDept)

	// 岗位
	post := rg.Group("/system/post")
	post.GET("/page", h.RequirePermission("system:post:query"), h.PagePosts)
	post.GET("/simple-list", h.ListSimplePosts)
	post.POST("", h.RequirePermission("system:post:create"), h.CreatePost)
	post.PUT("", h.RequirePermission("system:post:update"), h.UpdatePost)
	post.DELETE("/:id", h.RequirePermission("system:post:delete"), h.DeletePost)

	// 字典类型
	dictType := rg.Group("/system/dict-type")
	dictType.GET("/page", h.RequirePermission("system:dict:query"), h.PageDictTypes)
	dictType.POST("", h.RequirePermission("system:dict:create"), h.CreateDictType)
	dictType.PUT("", h.RequirePermission("system:dict:update"), h.UpdateDictType)
	dictType.DELETE("/:id", h.RequirePermission("system:dict:delete"), h.DeleteDictType)

	// 字典数据
	dictData := rg.Group("/system/dict-data")
	dictData.GET("/list", h.RequirePermission("system:dict:query"), h.ListDictData)
	dictData.GET("/type/:dictType", h.ListDictDataByType)
	dictData.POST("", h.RequirePermission("system:dict:create"), h.CreateDictData)
	dictData.PUT("", h.RequirePermission("system:dict:update"), h.UpdateDictData)
	dictData.DELETE("/:id", h.RequirePermission("system:dict:delete"), h.DeleteDictData)

	// 租户：仅平台租户可用（PR #39 残余风险 B）。中间件必须在所有路由前注册。
	tenantGroup := rg.Group("/system/tenant")
	tenantGroup.Use(h.RequirePlatformTenant())
	tenantGroup.GET("/page", h.RequirePermission("system:tenant:query"), h.PageTenants)
	tenantGroup.GET("/:id", h.RequirePermission("system:tenant:query"), h.GetTenant)
	tenantGroup.POST("", h.RequirePermission("system:tenant:create"), h.CreateTenant)
	tenantGroup.PUT("", h.RequirePermission("system:tenant:update"), h.UpdateTenant)
	tenantGroup.PUT("/status", h.RequirePermission("system:tenant:update"), h.UpdateTenantStatus)
	tenantGroup.DELETE("/:id", h.RequirePermission("system:tenant:delete"), h.DeleteTenant)

	// 登录日志
	loginLog := rg.Group("/system/login-log")
	loginLog.GET("/page", h.RequirePermission("system:login-log:query"), h.PageLoginLogs)
	loginLog.DELETE("/clear", h.RequirePermission("system:login-log:delete"), h.ClearLoginLogs)

	// 操作日志
	operateLog := rg.Group("/system/operate-log")
	operateLog.GET("/page", h.RequirePermission("system:operate-log:query"), h.PageOperateLogs)
	operateLog.DELETE("", h.RequirePermission("system:operate-log:delete"), h.DeleteOperateLogs)
	operateLog.DELETE("/clear", h.RequirePermission("system:operate-log:delete"), h.ClearOperateLogs)
}
