package api

import "github.com/gin-gonic/gin"

// RegisterPublic 注册无需登录的接口。
func (h *Handler) RegisterPublic(rg *gin.RouterGroup) {
	auth := rg.Group("/system/auth")
	auth.POST("/login", h.Login)
	auth.GET("/captcha", h.Captcha)

	// 社交登录（issue #76，spec §4.4/§5.3）：授权地址签发与三方回调均为
	// 公开端点；安全靠服务端一次性 state（10 分钟 TTL）校验，回调失败
	// 一律 400/409，不泄露内部错误（spec §4.11.3）。authorize-url 每次调用
	// 都写一条进程内 state，挂按 IP 限速（#85 安全审计 NV1；反代层可另配）。
	social := rg.Group("/system/social")
	social.GET("/authorize-url", h.LimitIP(), h.SocialAuthorizeURL)
	social.GET("/callback", h.SocialCallback)
	// 登录页可用平台列表（issue #77）：按启用客户端 + 已注册 provider 过滤。
	social.GET("/providers", h.ListSocialProviders)
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
	user.GET("/simple-list", h.RequirePermission("system:user:simple-list"), h.ListSimpleUsers)
	user.GET("/:id", h.RequirePermission("system:user:query"), h.GetUser)
	user.POST("", h.RequirePermission("system:user:create"), h.CreateUser)
	user.PUT("", h.RequirePermission("system:user:update"), h.UpdateUser)
	user.PUT("/status", h.RequirePermission("system:user:update"), h.UpdateUserStatus)
	user.PUT("/password", h.RequirePermission("system:user:update"), h.ResetUserPassword)
	user.DELETE("/:id", h.RequirePermission("system:user:delete"), h.DeleteUser)

	// 角色
	role := rg.Group("/system/role")
	role.GET("/page", h.RequirePermission("system:role:query"), h.PageRoles)
	role.GET("/simple-list", h.RequirePermission("system:role:simple-list"), h.ListSimpleRoles)
	role.GET("/:id", h.RequirePermission("system:role:query"), h.GetRole)
	role.POST("", h.RequirePermission("system:role:create"), h.CreateRole)
	role.PUT("", h.RequirePermission("system:role:update"), h.UpdateRole)
	role.DELETE("/:id", h.RequirePermission("system:role:delete"), h.DeleteRole)

	// 菜单：定义是全局共享的（system_menu 无 tenant_id），写操作只允许平台租户；
	// 租户侧只读列表会按套餐过滤（service.ListMenuTree）。否则租户可改全局菜单
	// 影响其他租户，套餐授权也会被绕过（ADR 0012）。
	menu := rg.Group("/system/menu")
	menu.GET("/list", h.RequirePermission("system:menu:query"), h.ListMenus)
	menu.GET("/:id", h.RequirePermission("system:menu:query"), h.GetMenu)
	menu.POST("", h.RequirePlatformTenant(), h.RequirePermission("system:menu:create"), h.CreateMenu)
	menu.PUT("", h.RequirePlatformTenant(), h.RequirePermission("system:menu:update"), h.UpdateMenu)
	menu.DELETE("/:id", h.RequirePlatformTenant(), h.RequirePermission("system:menu:delete"), h.DeleteMenu)

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
	post.GET("/simple-list", h.RequirePermission("system:post:simple-list"), h.ListSimplePosts)
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

	// 租户配额（issue #71，spec §5.1）：当前租户登录即可；指定租户需要
	// system:tenant:query，但不进下面的平台 403 组——非平台租户查别租户
	// 按「跨租户不可见」返回 404（handler 内判定，spec §4.11.4）。
	rg.GET("/system/tenant/quota", h.GetCurrentTenantQuota)
	rg.GET("/system/tenant/:id/quota", h.RequirePermission("system:tenant:query"), h.GetTenantQuota)

	// 租户：仅平台租户可用（PR #39 残余风险 B）。中间件必须在所有路由前注册。
	tenantGroup := rg.Group("/system/tenant")
	tenantGroup.Use(h.RequirePlatformTenant())
	tenantGroup.GET("/page", h.RequirePermission("system:tenant:query"), h.PageTenants)
	tenantGroup.GET("/:id", h.RequirePermission("system:tenant:query"), h.GetTenant)
	tenantGroup.POST("", h.RequirePermission("system:tenant:create"), h.CreateTenant)
	tenantGroup.PUT("", h.RequirePermission("system:tenant:update"), h.UpdateTenant)
	tenantGroup.PUT("/status", h.RequirePermission("system:tenant:update"), h.UpdateTenantStatus)
	tenantGroup.DELETE("/:id", h.RequirePermission("system:tenant:delete"), h.DeleteTenant)

	// 租户套餐（issue #69）：平台级资源，仅平台租户可管理。
	tenantPackage := rg.Group("/system/tenant-package")
	tenantPackage.Use(h.RequirePlatformTenant())
	tenantPackage.GET("/page", h.RequirePermission("system:tenant-package:query"), h.PageTenantPackages)
	tenantPackage.GET("/simple-list", h.RequirePermission("system:tenant-package:query"), h.ListSimpleTenantPackages)
	tenantPackage.GET("/:id", h.RequirePermission("system:tenant-package:query"), h.GetTenantPackage)
	tenantPackage.POST("", h.RequirePermission("system:tenant-package:create"), h.CreateTenantPackage)
	tenantPackage.PUT("", h.RequirePermission("system:tenant-package:update"), h.UpdateTenantPackage)
	tenantPackage.PUT("/status", h.RequirePermission("system:tenant-package:update"), h.UpdateTenantPackageStatus)
	tenantPackage.DELETE("/:id", h.RequirePermission("system:tenant-package:delete"), h.DeleteTenantPackage)

	// 社交登录（issue #76，spec §5.3）：绑定发起登录即可；绑定用户查询/解绑
	// 按权限码收口；客户端 CRUD 仅平台租户（平台级资源，同租户管理面）。
	social := rg.Group("/system/social")
	social.POST("/bind", h.SocialBind)
	// 个人中心「我的社交绑定」（issue #77，登录即可）：列表与解绑本人，
	// 无需权限码（只能操作本人绑定，服务层校验属主）。
	social.GET("/my", h.ListMySocialBindings)
	social.DELETE("/my/:id", h.UnbindMySocialBinding)
	socialUser := rg.Group("/system/social/user")
	socialUser.GET("/page", h.RequirePermission("system:social-user:query"), h.PageSocialUsers)
	socialUser.DELETE("/:id", h.RequirePermission("system:social-user:delete"), h.DeleteSocialUser)
	socialClient := rg.Group("/system/social/client")
	socialClient.Use(h.RequirePlatformTenant())
	socialClient.GET("/page", h.RequirePermission("system:social:query"), h.PageSocialClients)
	socialClient.POST("", h.RequirePermission("system:social:create"), h.CreateSocialClient)
	socialClient.PUT("", h.RequirePermission("system:social:update"), h.UpdateSocialClient)
	socialClient.DELETE("/:id", h.RequirePermission("system:social:delete"), h.DeleteSocialClient)

	// 地区（issue #82，spec §4.8/§5.7）：行政区划参考数据，树/子级开放给
	// 所有登录用户（spec §4.8.1，同部门树不设 RequirePermission，供级联
	// 选择器在任意页面使用）；导入仅平台租户 + system:area:import 权限码
	//（service 层再做平台校验兜底，同 db-doc 模式）。
	area := rg.Group("/system/area")
	area.GET("/tree", h.ListAreaTree)
	area.GET("/children", h.ListAreaChildren)
	area.POST("/import", h.RequirePlatformTenant(), h.RequirePermission("system:area:import"), h.ImportAreas)

	// 敏感词（issue #81，spec §5.6）：租户级词库 CRUD 与检测，
	// 权限码 system:sensitive-word:*（菜单 112，按钮 1121–1123）。
	sensitiveWord := rg.Group("/system/sensitive-word")
	sensitiveWord.GET("/page", h.RequirePermission("system:sensitive-word:query"), h.PageSensitiveWords)
	sensitiveWord.POST("/check", h.RequirePermission("system:sensitive-word:query"), h.CheckSensitiveWord)
	sensitiveWord.POST("", h.RequirePermission("system:sensitive-word:create"), h.CreateSensitiveWord)
	sensitiveWord.PUT("", h.RequirePermission("system:sensitive-word:update"), h.UpdateSensitiveWord)
	sensitiveWord.DELETE("/:id", h.RequirePermission("system:sensitive-word:delete"), h.DeleteSensitiveWord)

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
