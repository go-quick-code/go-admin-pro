// Package dto 是 oa 模块的请求/响应 DTO。
package dto

import (
	"io"
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
type AnnouncementItemResp struct {
	ID           int64      `json:"id"`
	Title        string     `json:"title"`
	Status       int8       `json:"status"`
	AudienceType int8       `json:"audienceType"`
	Pinned       bool       `json:"pinned"`
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

// AnnouncementFileResp 是公告附件视图（infra_file，biz_type='oa_announcement'）。
type AnnouncementFileResp struct {
	ID         int64     `json:"id"`
	Name       string    `json:"name"`
	URL        string    `json:"url"`
	Size       int64     `json:"size"`
	Type       string    `json:"type"`
	CreateTime time.Time `json:"createTime"`
}

// AnnouncementFileContent 是公告附件的下载内容（已通过公告可见性与文件归属校验）。
// Reader 由调用方负责关闭（API 层流式写出后关闭）。
type AnnouncementFileContent struct {
	Name        string
	ContentType string
	Size        int64
	Reader      io.ReadCloser
}
