// Package dto 是 oa 模块的请求/响应 DTO。
package dto

import (
	"time"

	"github.com/go-quick-code/go-admin-pro/internal/pkg/web"
)

// ---------- 公告（issue #48，docs/oa-mvp-spec.md §3.4/§4.7/§5）----------

// AnnouncementPageReq 是公告管理分页筛选（GET /oa/announcement/page，
// oa:announcement:query）：status/audienceType/pinned 精确，keyword 匹配标题。
type AnnouncementPageReq struct {
	web.PageQuery
	Status       *int8  `form:"status"`
	AudienceType *int8  `form:"audienceType"`
	Pinned       *bool  `form:"pinned"`
	Keyword      string `form:"keyword"`
}

// AnnouncementMyPageReq 是员工「我的公告」分页（GET /oa/announcement/my/page，
// 登录即可）：只返回已发布且命中受众的公告，keyword 匹配标题。
type AnnouncementMyPageReq struct {
	web.PageQuery
	Keyword string `form:"keyword"`
}

// AnnouncementTargetReq 是一条受众目标：targetType 1 部门（含子树）/ 2 角色。
type AnnouncementTargetReq struct {
	TargetType int8  `json:"targetType"`
	TargetID   int64 `json:"targetId"`
}

// AnnouncementCreateReq 是公告创建请求（POST /oa/announcement）：
// 新建即草稿；content 由服务端 bluemonday 清洗后入库。
type AnnouncementCreateReq struct {
	Title        string                  `json:"title" binding:"required"`
	Content      string                  `json:"content"`
	AudienceType int8                    `json:"audienceType"`
	Targets      []AnnouncementTargetReq `json:"targets"`
	Pinned       bool                    `json:"pinned"`
}

// AnnouncementUpdateReq 是公告更新请求（PUT /oa/announcement，仅草稿）：
// 受众目标整体替换（先删后插，同事务）。
type AnnouncementUpdateReq struct {
	ID           int64                   `json:"id" binding:"required"`
	Title        string                  `json:"title" binding:"required"`
	Content      string                  `json:"content"`
	AudienceType int8                    `json:"audienceType"`
	Targets      []AnnouncementTargetReq `json:"targets"`
	Pinned       bool                    `json:"pinned"`
}

// AnnouncementTargetResp 是受众目标视图（附部门/角色名称）。
type AnnouncementTargetResp struct {
	TargetType int8   `json:"targetType"`
	TargetID   int64  `json:"targetId"`
	TargetName string `json:"targetName"`
}

// AnnouncementItemResp 是公告列表行（管理列表与我的公告共用；不含正文）。
// Read 是当前查看者是否已读（打开详情即写入，issue #49）；Unread 是列表
// 未读标记，只有「已发布 + 无已读记录」为 true（草稿/已撤回恒 false）。
type AnnouncementItemResp struct {
	ID           int64      `json:"id"`
	Title        string     `json:"title"`
	Status       int8       `json:"status"`
	AudienceType int8       `json:"audienceType"`
	Pinned       bool       `json:"pinned"`
	Read         bool       `json:"read"`
	Unread       bool       `json:"unread"`
	PublishedAt  *time.Time `json:"publishedAt"`
	WithdrawnAt  *time.Time `json:"withdrawnAt"`
	CreateTime   time.Time  `json:"createTime"`
	UpdateTime   time.Time  `json:"updateTime"`
}

// AnnouncementDetailResp 是公告详情（GET /oa/announcement/{id}）：
// content 为清洗后的 HTML；targets 为自定义受众（全员时为空数组）。
type AnnouncementDetailResp struct {
	AnnouncementItemResp
	Content string                   `json:"content"`
	Targets []AnnouncementTargetResp `json:"targets"`
}

// ---------- 公告已读统计（issue #49，docs/oa-mvp-spec.md §4.7）----------

// AnnouncementReadStatsReq 是已读名单分页请求
// （GET /oa/announcement/{id}/read-stats，oa:announcement:read-stats）。
type AnnouncementReadStatsReq struct {
	web.PageQuery
}

// AnnouncementReadUserResp 是已读名单的一行：员工 ID、昵称与首次已读时间。
type AnnouncementReadUserResp struct {
	UserID   int64     `json:"userId"`
	Nickname string    `json:"nickname"`
	ReadAt   time.Time `json:"readAt"`
}

// AnnouncementReadStatsResp 是已读统计响应：readCount 与 total 同口径，
// 都是按 user_id 去重后的已读人数（唯一键保证至多一行/人）；list 为当前页
// 名单，按首次已读时间升序。
type AnnouncementReadStatsResp struct {
	ReadCount int64                      `json:"readCount"`
	Total     int64                      `json:"total"`
	List      []AnnouncementReadUserResp `json:"list"`
}
