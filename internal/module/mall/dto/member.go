package dto

import (
	"github.com/go-quick-code/go-admin-pro/internal/module/mall/model"
)

// member.go 是会员域的请求/响应 DTO（issue #174，docs/mall-mvp-spec.md §4）。
// 列表/详情返回直接复用 service 层的 model.MemberUser（JSON 契约即模型
// 序列化，同 pay 应用域；password_hash 带 json:"-" 天然不出网）。

// MemberPageReq 会员分页筛选（GET /mall/member/page，权限 mall:member:query）。
type MemberPageReq struct {
	PageNo   int    `form:"pageNo"`
	PageSize int    `form:"pageSize"`
	Mobile   string `form:"mobile"`
	Nickname string `form:"nickname"`
	LevelID  *int64 `form:"levelId"`
	Status   *int8  `form:"status"`
}

// MemberSaveReq 创建会员（POST /mall/member）：mobile 租户内唯一（撞唯一键
// 409，issue #174）。password 选填：空 = 不设密码（C 端注册入口随 T2 落地
// 后自设）；填了须 6–64 位，服务端 bcrypt 散列后落 password_hash。
// point_balance/等级归属的服务端字段：level_id 可指派，point 余额不收
// （唯一入账入口维护，T2）。
type MemberSaveReq struct {
	Mobile    string `json:"mobile" binding:"required,max=20"`
	Password  string `json:"password" binding:"max=64"`
	Nickname  string `json:"nickname" binding:"max=64"`
	AvatarURL string `json:"avatarUrl" binding:"max=500"`
	LevelID   int64  `json:"levelId"`
	Status    *int8  `json:"status"`
	Remark    string `json:"remark" binding:"max=500"`
}

// MemberUpdateReq 修改会员（PUT /mall/member，id 在 body）：password 空 =
// 不改密码；mobile 可改，唯一键兜底并发冲突（409）。
type MemberUpdateReq struct {
	ID int64 `json:"id" binding:"required"`
	MemberSaveReq
}

// MemberStatusReq 停启用会员（PUT /mall/member/:id/status，权限
// mall:member:update——状态位更新随更新码，不设独立码）。Status 用指针：
// 0 启用是合法值，required 语义对非指针零值失效。
type MemberStatusReq struct {
	Status *int8 `json:"status" binding:"required"`
}

// MemberDetail 会员详情（GET /mall/member/:id）：基本信息 + 收货地址清单
// （按会员查地址，spec §4「地址列表（按会员）」挂详情一次带出，管理端无
// 地址写入端点）。
type MemberDetail struct {
	*model.MemberUser
	Addresses []model.MemberAddress `json:"addresses"`
}
