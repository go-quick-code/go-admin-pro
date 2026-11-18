// Package service 是 mall 模块的业务层（issue #174：骨架与会员域管理端）。
//
// 规则来源：docs/mall-mvp-spec.md §1/§2/§3.1/§4/§7；tracker issue #174。
//   - 会员（member_user）：管理端 CRUD + 停启用；mobile 租户内唯一（撞键
//     409）；密码 bcrypt 散列（照 system_user），明文不落库不回传；会员无
//     删除端点（停启用覆盖生命周期，业务数据——积分流水/订单——随会员
//     存续）。积分余额（point_balance）只经 T2 唯一入账入口变动，本层不提供
//     任何直改路径。
//   - 等级（member_level）：CRUD，name/level 双租户内唯一（撞键 409）；
//     删除守卫「被会员引用禁删」（409）。等级纯手工维护（MVP 无经验值体系）。
//   - 地址（member_address）：按会员查（只读），写入随 C 端自助（T2/T5）。
//   - 积分（member_point_config/record）：表结构随 T1 迁移一次建全，唯一
//     入账入口（照 pay wallet 流水先例）随 T2 落地——本层无任何积分逻辑。
//
// 分层形状对照 pay：dto 收请求、repository 收查询、service 收规则与事务、
// api 收 HTTP；字段级校验纯函数见 member_rules.go（外部测试包测试）。
package service

import (
	mallrepo "github.com/go-quick-code/go-admin-pro/internal/module/mall/repository"
)

// Service 汇聚 mall 模块的会员域业务能力。
type Service struct {
	repo *mallrepo.Repository
}

// New 创建 mall Service。
func New(repo *mallrepo.Repository) *Service {
	return &Service{repo: repo}
}
