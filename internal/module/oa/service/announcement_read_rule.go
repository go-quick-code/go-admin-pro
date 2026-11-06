// announcement_read_rule.go 承载公告已读的纯判定与统计口径（issue #49，
// docs/oa-mvp-spec.md §4.7）：未读标记只在「已发布 + 无已读记录」时显示；
// 已读名单按 user_id 去重并保留首次 read_at（与 oa_announcement_read 的
// 唯一键 (tenant_id, announcement_id, user_id) 同语义，库层唯一键是主保证，
// 本文件是统计口径的单一表达）。撤回/重新发布不删除已读记录，因此这些
// 函数不接收公告状态之外的任何「发布时间」输入。
package service

import (
	"sort"
	"time"

	"github.com/go-quick-code/go-admin-pro/internal/module/oa/model"
)

// AnnouncementUnread 报告公告对查看者是否应显示未读标记（spec §4.7）：
// 只有已发布公告存在「未读」语义；草稿仅管理端可见、已撤回不在员工列表，
// 两者都不显示未读。非法状态 fail-closed 返回 false。
func AnnouncementUnread(status int8, read bool) bool {
	return status == model.AnnouncementStatusPublished && !read
}

// AnnouncementReadUser 是已读名单的一行（存储无关）：用户与首次已读时间。
// 昵称由 service 经 system 网关补齐，不进入纯函数。
type AnnouncementReadUser struct {
	UserID int64
	ReadAt time.Time
}

// AggregateAnnouncementReadUsers 汇总已读名单（统计口径）：
//   - 同一用户出现多行只算一次，保留最早的 read_at（幂等写入不覆盖首次
//     已读时间；唯一键在库层保证至多一行，本函数对历史脏数据兜底）；
//   - 忽略非正 userID（异常上下文/脏数据不进入名单）；
//   - 输出按 (read_at, user_id) 升序，保证分页顺序稳定。
//
// 撤回与重新发布不触碰已读记录，因此调用方不需要传公告状态：历史已读
// 在撤回后仍计入统计，重新发布也不会重置。
func AggregateAnnouncementReadUsers(rows []AnnouncementReadUser) []AnnouncementReadUser {
	byUser := make(map[int64]time.Time, len(rows))
	for _, row := range rows {
		if row.UserID <= 0 {
			continue
		}
		existing, ok := byUser[row.UserID]
		if !ok || row.ReadAt.Before(existing) {
			byUser[row.UserID] = row.ReadAt
		}
	}
	out := make([]AnnouncementReadUser, 0, len(byUser))
	for userID, readAt := range byUser {
		out = append(out, AnnouncementReadUser{UserID: userID, ReadAt: readAt})
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].ReadAt.Equal(out[j].ReadAt) {
			return out[i].UserID < out[j].UserID
		}
		return out[i].ReadAt.Before(out[j].ReadAt)
	})
	return out
}
