package seed

import (
	"fmt"

	crmservice "github.com/go-quick-code/go-admin-pro/internal/module/crm/service"
	inframodel "github.com/go-quick-code/go-admin-pro/internal/module/infra/model"
	infraservice "github.com/go-quick-code/go-admin-pro/internal/module/infra/service"
	mallservice "github.com/go-quick-code/go-admin-pro/internal/module/mall/service"
	oaservice "github.com/go-quick-code/go-admin-pro/internal/module/oa/service"
	payservice "github.com/go-quick-code/go-admin-pro/internal/module/pay/service"
	"github.com/go-quick-code/go-admin-pro/internal/pkg/tenant"
	"gorm.io/gorm"
)

// JobSpec 是一条定时任务的规格（ADR 0024 补遗）：metadata-only 单源——
// 种子行（seedJobs）与种子测试断言从它投影，装配处（internal/app）按
// HandlerName 为它绑定执行闭包。规格与闭包分离：seed.Run 在模块服务装配
// 之前执行，带执行闭包的规格在该时点不存在（时序否决候选报告的
// JobSpec{…, Run} 原案），规格是种子策略、闭包是装配接线，生命周期不同。
type JobSpec struct {
	// HandlerName 处理器名：引用所属模块的常量（名字的编译期单一出处），
	// seed 规格与装配绑定引用同一常量，不再手抄字面量。
	HandlerName string
	// Name 显示名（infra_job.name 种子行）。
	Name string
	// CronExpression cron 表达式（6 段含秒，按应用时区解释）。
	CronExpression string
	// HandlerParam 默认任务参数（infra_job.handler_param 种子行）；仅
	// demoJob 有值，其余任务无默认参数留空。
	HandlerParam string
	// Status 默认种子状态：0 启用 / 1 暂停（inframodel.JobStatus*）。
	Status int8
	// Remark 备注（infra_job.remark 种子行）。
	Remark string
	// System 是否系统级（跨租户动作）：true 时只有平台租户管理员可
	// 创建/修改/启用/运行，普通租户处理器列表不展示——租户可见性安全边界
	//（标错 false 会把跨租户动作暴露给普通租户管理员），装配处按本标志
	// 分派 Register/RegisterSystem。
	System bool
}

// JobSpecs 全部种子任务的规格清单（ADR 0024 补遗，条目次序即种子行写入
// 次序）。新模块加定时任务在此追加一条规格 + 装配处 bindings map 追加一条
// 绑定 + 模块 service 提供 XxxJobHandlerName 常量与 RunXxx 方法（三处声明，
// 见 docs/agents/new-module.md 第 9 步）；规格缺绑定应用拒绝启动（fail-closed）。
var JobSpecs = []JobSpec{
	{
		HandlerName:    infraservice.DemoJobHandlerName,
		Name:           "演示任务",
		HandlerParam:   "hello go-admin-pro",
		CronExpression: "0 */5 * * * *",
		Status:         inframodel.JobStatusPaused,
		Remark:         "每 5 分钟执行一次的演示任务，默认暂停",
		// 非系统级：任何租户可建 demoJob 任务行（租户可见性零变化）。
		System: false,
	},
	// 公海自动回收（issue #14）：默认暂停。
	// 理由：回收是批量清空负责人归属的破坏性动作，触发前管理员应先
	// 按租户确认公海配置（回收天数，默认 30）。合同排除已由 #16 接入
	//（有未作废合同的客户不被回收），但回收仍属破坏性动作，确认配置后再启用。
	{
		HandlerName:    crmservice.RecycleJobHandlerName,
		Name:           "公海自动回收",
		CronExpression: "0 0 3 * * *",
		Status:         inframodel.JobStatusPaused,
		Remark:         "每天 03:00 按租户公海配置回收超期未跟进的客户（清空负责人并写历史），默认暂停：请先确认各租户回收天数配置",
		System:         true,
	},
	{
		HandlerName:    oaservice.AttendanceDailyResultHandlerName,
		Name:           "考勤日结果生成",
		CronExpression: "0 0 1 * * *",
		// 默认启用（spec §4.10「infra_job 每天 01:00 生成前一日结果」）：
		// 派生幂等且只写本租户结果，非破坏性；handler_param 可填 YYYY-MM-DD
		// 回填指定日期。
		Status: inframodel.JobStatusRunning,
		Remark: "每天 01:00 生成前一日考勤结果（按启用租户迭代）；handler_param 可填 YYYY-MM-DD 回填指定日期",
		System: true,
	},
	// 会议提醒（issue #57，spec §4.11）：每 5 分钟扫描 (now, now+15m]
	// 内开始的有效预约，向预约人写 meeting_reminder 通知；reminded_at
	// 条件更新保证只发一次。默认启用（非破坏性）。
	{
		HandlerName:    oaservice.RoomBookingReminderHandlerName,
		Name:           "会议提醒",
		CronExpression: "0 */5 * * * *",
		Status:         inframodel.JobStatusRunning,
		Remark:         "每 5 分钟扫描 15 分钟内开始的会议室预约，向预约人发送 meeting_reminder 通知（reminded_at 保证只发一次）；handler_param 可填 YYYY-MM-DD HH:mm:ss 固定现在",
		System:         true,
	},
	// 访问日志清理（issue #80，spec §4.6.7）：每日 03:00 按保留期清理
	// 跨租户的 infra_api_log（tenant.Skip）。保留期取配置
	// access-log.retention-days（默认 30），handler_param 可填天数覆盖；
	// 只删过期日志，按保留期例行清理，默认启用。
	{
		HandlerName:    infraservice.CleanAccessLogsJobHandlerName,
		Name:           "访问日志清理",
		CronExpression: "0 0 3 * * *",
		Status:         inframodel.JobStatusRunning,
		Remark:         "每天 03:00 清理超过保留期（access-log.retention-days，默认 30 天）的 API 访问日志（跨租户）；handler_param 可填天数覆盖保留期",
		System:         true,
	},
	// 商户回调重试（issue #134，spec §4.5/§7）：每分钟扫描到期任务
	//（status IN (0,21,22) 且 next_notify_time <= now）POST 商户回调地址，
	// 重试间隔 {15,15,30,180,1800,1800,1800,3600} 秒、首次 + 8 重试共 9 次。
	// 默认启用（投递是资金链路的正常环节；条件更新认领防重发，密钥缺失
	// 拒发 fail-closed，不会产生误发）。
	{
		HandlerName:    payservice.NotifyRetryJobHandlerName,
		Name:           "商户回调重试",
		CronExpression: "0 * * * * *",
		Status:         inframodel.JobStatusRunning,
		Remark:         "每分钟扫描到期商户回调任务并 POST 商户回调地址（重试间隔 {15,15,30,180,1800,1800,1800,3600} 秒，9 次失败置彻底失败）；回调密钥缺失时拒发（fail-closed）",
		System:         true,
	},
	// 订单超时关闭（issue #134，spec §7）：每 5 分钟扫描过期待支付订单
	//（status=0 且 expire_time < now）置 30 已关闭。默认启用（幂等条件
	// 更新，已支付/已关闭不受影响，非破坏性）。
	{
		HandlerName:    payservice.OrderTimeoutCloseJobHandlerName,
		Name:           "订单超时关闭",
		CronExpression: "0 */5 * * * *",
		Status:         inframodel.JobStatusRunning,
		Remark:         "每 5 分钟扫描过期待支付订单（expire_time < now）并置为已关闭；已支付/已关闭订单不受影响",
		System:         true,
	},
	// 商城订单超时关闭（issue #182，mall spec §1 第 7 条）：每 5 分钟扫描
	// 待支付超 15 分钟的订单（status=0 且 created_at < now−15min，业务时钟
	// businessNow 口径）置 40 已关闭（close_reason="超时未支付"）并同事务
	// 回补库存。默认启用（幂等条件更新，已支付/已取消/已关闭不受影响——
	// 终态无出边，非破坏性）。
	{
		HandlerName:    mallservice.OrderTimeoutCloseJobHandlerName,
		Name:           "商城订单超时关闭",
		CronExpression: "0 */5 * * * *",
		Status:         inframodel.JobStatusRunning,
		Remark:         "每 5 分钟扫描待支付超 15 分钟的商城订单并置为已关闭（同事务回补库存）；已支付/已取消/已关闭订单不受影响",
		System:         true,
	},
}

// seedJobs 从 JobSpecs 投影任务种子行：仅当 infra_job 为空时写入（幂等——
// 管理员删除/改名/启停后重复启动不复活）。TenantID 显式赋 1（seed 是系统级
// 写入路径，无租户上下文，不依赖插件回填）。
func seedJobs(db *gorm.DB) error {
	var count int64
	if err := tenant.Skip(db).Model(&inframodel.Job{}).Count(&count).Error; err != nil {
		return err
	}
	if count > 0 {
		return nil
	}
	jobs := make([]inframodel.Job, 0, len(JobSpecs))
	for _, spec := range JobSpecs {
		jobs = append(jobs, inframodel.Job{
			TenantID:       1,
			Name:           spec.Name,
			HandlerName:    spec.HandlerName,
			HandlerParam:   spec.HandlerParam,
			CronExpression: spec.CronExpression,
			RetryCount:     0,
			Status:         spec.Status,
			Remark:         spec.Remark,
		})
	}
	// Create 会把带 default 标签的零值列回写成数据库默认值（1 暂停），
	// 因此在插入前先快照种子的意图状态，再逐条用 Exec 显式回写。
	statusByHandler := make(map[string]int8, len(JobSpecs))
	for _, spec := range JobSpecs {
		statusByHandler[spec.HandlerName] = spec.Status
	}
	if err := tenant.Skip(db).Create(&jobs).Error; err != nil {
		return err
	}
	// GORM 对带 `default:1` 标签的零值 Status 会走「省略列 → 落库默认值」，
	// 导致「默认启用」的任务实际以暂停状态入库（issue #63 收口 #57 遗留）。
	// 逐条用 Exec 显式回写种子意图的状态：无论 0（启用）还是 1（暂停）都以
	// 种子为准，避免 ORM 对带 default 标签列的省略行为影响最终状态。
	for handlerName, status := range statusByHandler {
		if err := tenant.Skip(db).Exec(
			"UPDATE `infra_job` SET `status` = ? WHERE `tenant_id` = ? AND `handler_name` = ?",
			status, 1, handlerName,
		).Error; err != nil {
			return fmt.Errorf("seed job status %s: %w", handlerName, err)
		}
	}
	return nil
}
