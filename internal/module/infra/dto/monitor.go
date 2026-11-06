package dto

// 监控面板响应（issue #84，spec §4.10/§5.8）。只读聚合，无写入端点；
// 字段全部 camelCase，与前端 /web/src/views/infra/monitor/index.vue 对齐。

// MonitorAppInfo 是应用基本信息。
type MonitorAppInfo struct {
	// Name/Env 来自配置 app.name / app.env。
	Name string `json:"name"`
	Env  string `json:"env"`
	// Version 是构建版本：嵌入 VCS revision 时取短哈希，否则回退 dev。
	Version string `json:"version"`
	// GoVersion 是编译本进程的 Go 运行时版本（runtime.Version()）。
	GoVersion string `json:"goVersion"`
	// StartTime 是进程启动时间（YYYY-MM-DD HH:mm:ss，业务时区）。
	StartTime string `json:"startTime"`
	// UptimeSeconds 是运行时长（秒）。
	UptimeSeconds int64 `json:"uptimeSeconds"`
}

// MonitorRuntimeInfo 是 Go 运行期指标。
type MonitorRuntimeInfo struct {
	// Goroutines 是当前 goroutine 数。
	Goroutines int `json:"goroutines"`
	// HeapBytes 是堆上存活对象字节数（MemStats.HeapAlloc）。
	HeapBytes int64 `json:"heapBytes"`
	// SysBytes 是向操作系统申请的内存字节数（MemStats.Sys）。
	SysBytes int64 `json:"sysBytes"`
	// GCCount 是累计 GC 次数（MemStats.NumGC 截断回绕后）。
	GCCount int64 `json:"gcCount"`
}

// MonitorDBPoolInfo 是数据库连接池指标（database/sql Stats + 配置上限）。
type MonitorDBPoolInfo struct {
	MaxOpen int `json:"maxOpen"`
	Open    int `json:"open"`
	InUse   int `json:"inUse"`
	Idle    int `json:"idle"`
	// WaitCount 是累计等待连接的次数；WaitMillis 是累计等待毫秒数。
	WaitCount  int64 `json:"waitCount"`
	WaitMillis int64 `json:"waitMillis"`
}

// MonitorJobInfo 是调度器概览。
type MonitorJobInfo struct {
	// Total/Enabled 是 infra_job 全租户的任务总数与启用数（status=0）。
	Total   int64 `json:"total"`
	Enabled int64 `json:"enabled"`
	// HandlerCount 是本进程注册的 handler 数（含系统级）。
	HandlerCount int `json:"handlerCount"`
}

// MonitorTenantInfo 是租户与用户规模（跨模块统计，经窄接口注入）。
type MonitorTenantInfo struct {
	Count int64 `json:"count"`
	Users int64 `json:"users"`
}

// MonitorTrafficInfo 是近 24h 访问流量概览（来自 infra_api_log 全租户聚合）。
type MonitorTrafficInfo struct {
	// Total 是近 24h 请求数。
	Total int64 `json:"total"`
	// ErrorRate 是错误率百分比（4xx+5xx 占比，保留 1 位小数）。
	ErrorRate float64 `json:"errorRate"`
	// AvgMs/MaxMs 是平均与最大耗时（毫秒）。
	AvgMs int64 `json:"avgMs"`
	MaxMs int64 `json:"maxMs"`
}

// MonitorMessageInfo 是近 24h 消息发送结果（跨模块统计，经窄接口注入）。
type MonitorMessageInfo struct {
	SmsSuccess  int64 `json:"smsSuccess"`
	SmsFailed   int64 `json:"smsFailed"`
	MailSuccess int64 `json:"mailSuccess"`
	MailFailed  int64 `json:"mailFailed"`
}

// MonitorOverviewResp 是 GET /infra/monitor/overview 的响应。
type MonitorOverviewResp struct {
	App      MonitorAppInfo     `json:"app"`
	Runtime  MonitorRuntimeInfo `json:"runtime"`
	DBPool   MonitorDBPoolInfo  `json:"dbPool"`
	Jobs     MonitorJobInfo     `json:"jobs"`
	Tenants  MonitorTenantInfo  `json:"tenants"`
	Traffic  MonitorTrafficInfo `json:"traffic"`
	Messages MonitorMessageInfo `json:"messages"`
}

// MonitorRequestBucket 是 requests 按小时分桶的单桶聚合。
type MonitorRequestBucket struct {
	// Hour 是桶起点（整点，YYYY-MM-DD HH:00，业务时区）。
	Hour string `json:"hour"`
	// Total 是该小时请求数；S2xx~S5xx 是 2xx/3xx/4xx/5xx 计数。
	Total int64 `json:"total"`
	S2xx  int64 `json:"s2xx"`
	S3xx  int64 `json:"s3xx"`
	S4xx  int64 `json:"s4xx"`
	S5xx  int64 `json:"s5xx"`
	// AvgMs/MaxMs 是该小时平均与最大耗时（毫秒，四舍五入）。
	AvgMs int64 `json:"avgMs"`
	MaxMs int64 `json:"maxMs"`
}

// MonitorRequestsResp 是 GET /infra/monitor/requests 的响应。
type MonitorRequestsResp struct {
	// Hours 是回显的聚合窗口（小时，已收敛到 [1,168]）。
	Hours int `json:"hours"`
	// Buckets 是非空小时桶（按时间升序）；无数据时为空数组而非 null。
	Buckets []MonitorRequestBucket `json:"buckets"`
}
