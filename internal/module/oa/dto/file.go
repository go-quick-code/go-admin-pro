// file.go 是 OA 各业务附件视图的共用 DTO（issue #64 code-review P2）：
// 公告/请假/报销/文档四个附件链路此前各自声明了字段完全相同的响应结构，
// 这里收敛为 FileResp / FileContent，业务侧保留类型别名以维持既有 API
// 与调用方命名（AnnouncementFileResp、LeaveFileResp、...）。
package dto

import (
	"io"
	"time"
)

// FileResp 是业务附件视图（infra_file 按 biz_type/biz_id 归属）：
// URL 由业务模块改写为鉴权下载端点，不暴露存储实现字段。
type FileResp struct {
	ID         int64     `json:"id"`
	Name       string    `json:"name"`
	URL        string    `json:"url"`
	Size       int64     `json:"size"`
	Type       string    `json:"type"`
	CreateTime time.Time `json:"createTime"`
}

// FileContent 是业务附件的下载内容（已通过业务可见性与文件归属校验）。
// Reader 由调用方负责关闭（API 层流式写出后关闭）。
type FileContent struct {
	Name        string
	ContentType string
	Size        int64
	Reader      io.ReadCloser
}

// 业务侧别名：保持既有 DTO 名称与 JSON 结构不变。
type AnnouncementFileResp = FileResp
type LeaveFileResp = FileResp
type ExpenseFileResp = FileResp

type AnnouncementFileContent = FileContent
type DocumentFileContent = FileContent
