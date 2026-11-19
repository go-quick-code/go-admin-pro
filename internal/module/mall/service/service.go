// Package service 是 mall 模块的业务层（issue #174 骨架 + issue #175 积分域）。
//
// 规则来源：docs/mall-mvp-spec.md §1/§2/§3.1/§4/§7；tracker issue #174/#175。
//   - 会员（member_user）：管理端 CRUD + 停启用；mobile 租户内唯一（撞键
//     409）；密码 bcrypt 散列（照 system_user），明文不落库不回传；会员无
//     删除端点（停启用覆盖生命周期，业务数据——积分流水/订单——随会员
//     存续）。
//   - 积分（member_point_config/record，#175）：member_user.point_balance
//     只经唯一入账入口（point.go ApplyPointChange，照 pay wallet 先例）变动，
//     任何其它路径不得直改；配置租户级单行惰性 upsert；biz_type 常量三类
//     先定义，order_pay/order_cancel 调用方随 T4/T3/T9 接线。
//   - 等级（member_level）：CRUD，name/level 双租户内唯一（撞键 409）；
//     删除守卫「被会员引用禁删」（409）。等级纯手工维护（MVP 无经验值体系）。
//   - 地址（member_address）：按会员查（只读），写入随 C 端自助（T5）。
//
// 分层形状对照 pay：dto 收请求、repository 收查询、service 收规则与事务、
// api 收 HTTP；字段级校验纯函数见 member_rules.go / point_rules.go
// （外部测试包测试）。
package service

import (
	"context"
	"time"

	mallrepo "github.com/go-quick-code/go-admin-pro/internal/module/mall/repository"
	"github.com/go-quick-code/go-admin-pro/internal/pkg/authn"
)

// Service 汇聚 mall 模块的业务能力。
type Service struct {
	repo *mallrepo.Repository
	// location 是业务时区（ADR 0006），由 app 装配时 SetLocation 注入：
	// SO 单号日期键一律按它划分（trade.go businessNow）。nil 时退回 time.Local。
	location *time.Location
	// memberTokenSecret/memberTokenTTL 是 C 端会员令牌（issue #178，T5）的
	// 签名材料：app 装配期经 SetMemberTokenAuth 注入（config mall.jwt-secret /
	// mall.token-ttl）。密钥缺失在 app.New 装配首检 fail-closed 拒启；
	// 签发/校验侧再防御（见 member_token.go memberTokenReady）。
	memberTokenSecret []byte
	memberTokenTTL    time.Duration
	// tenants 是启用租户清单来源（issue #182，T9 超时关单 job 的跨租户迭代
	// 数据源）：app 装配期经 SetTenantLister 注入（system Service 直接满足
	// TenantLister 窄接口，照 pay 同型）。未注入时 job 报错拒跑（fail-closed）。
	tenants TenantLister
}

// New 创建 mall Service。
func New(repo *mallrepo.Repository) *Service {
	return &Service{repo: repo}
}

// mallTenantID 取当前请求主体的租户 ID（入账原语走显式租户参数裸 SQL 的
// 租户来源，照 payTenantID 先例）：无登录主体返回 0，调用方 fail-closed。
func mallTenantID(ctx context.Context) int64 {
	if p, ok := authn.From(ctx); ok {
		return p.TenantID
	}
	return 0
}
