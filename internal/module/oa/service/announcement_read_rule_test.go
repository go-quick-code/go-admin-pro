// announcement_read_rule_test.go 以表驱动锁定公告已读的判定与统计口径
// （issue #49，docs/oa-mvp-spec.md §4.7）：未读标记只在「已发布 + 无已读
// 记录」时显示；已读名单按 user_id 去重并保留首次 read_at（与
// oa_announcement_read 唯一键同语义），撤回/重新发布不改变已读记录。
package service

import (
	"testing"
	"time"

	"github.com/go-quick-code/go-admin-pro/internal/module/oa/model"
)

// TestAnnouncementUnread 覆盖未读标记的状态组合：只有已发布且没有
// 已读记录时才是未读；草稿与已撤回不显示未读；非法状态 fail-closed。
func TestAnnouncementUnread(t *testing.T) {
	cases := []struct {
		name   string
		status int8
		read   bool
		want   bool
	}{
		{name: "草稿_无已读_不显示未读", status: model.AnnouncementStatusDraft, read: false, want: false},
		{name: "草稿_有已读_不显示未读", status: model.AnnouncementStatusDraft, read: true, want: false},
		{name: "已发布_无已读_显示未读", status: model.AnnouncementStatusPublished, read: false, want: true},
		{name: "已发布_有已读_不显示未读", status: model.AnnouncementStatusPublished, read: true, want: false},
		{name: "已撤回_无已读_不显示未读", status: model.AnnouncementStatusWithdrawn, read: false, want: false},
		{name: "已撤回_有已读_不显示未读", status: model.AnnouncementStatusWithdrawn, read: true, want: false},
		{name: "非法状态_不显示未读", status: int8(9), read: false, want: false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := AnnouncementUnread(tc.status, tc.read); got != tc.want {
				t.Fatalf("AnnouncementUnread(%d, %v) = %v, want %v", tc.status, tc.read, got, tc.want)
			}
		})
	}
}

// TestAggregateAnnouncementReadUsers 锁定已读名单的统计口径：
//   - 同一用户多行只算一次（保留最早 read_at；库层唯一键的兜底）；
//   - 非正 userID 忽略；
//   - 输出按 (read_at, user_id) 升序，保证分页稳定。
func TestAggregateAnnouncementReadUsers(t *testing.T) {
	t0 := time.Date(2026, 11, 6, 9, 0, 0, 0, time.UTC)
	t1 := t0.Add(time.Minute)
	t2 := t0.Add(2 * time.Minute)

	cases := []struct {
		name string
		rows []AnnouncementReadUser
		want []AnnouncementReadUser
	}{
		{
			name: "空名单",
			rows: nil,
			want: []AnnouncementReadUser{},
		},
		{
			name: "同一用户重复行只算一次且保留最早已读时间",
			rows: []AnnouncementReadUser{
				{UserID: 7, ReadAt: t2},
				{UserID: 7, ReadAt: t0},
				{UserID: 8, ReadAt: t1},
			},
			want: []AnnouncementReadUser{
				{UserID: 7, ReadAt: t0},
				{UserID: 8, ReadAt: t1},
			},
		},
		{
			name: "忽略非正用户ID",
			rows: []AnnouncementReadUser{
				{UserID: 0, ReadAt: t0},
				{UserID: -3, ReadAt: t0},
				{UserID: 5, ReadAt: t1},
			},
			want: []AnnouncementReadUser{{UserID: 5, ReadAt: t1}},
		},
		{
			name: "同一时间按用户ID升序",
			rows: []AnnouncementReadUser{
				{UserID: 9, ReadAt: t1},
				{UserID: 3, ReadAt: t1},
				{UserID: 6, ReadAt: t0},
			},
			want: []AnnouncementReadUser{
				{UserID: 6, ReadAt: t0},
				{UserID: 3, ReadAt: t1},
				{UserID: 9, ReadAt: t1},
			},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := AggregateAnnouncementReadUsers(tc.rows)
			if len(got) != len(tc.want) {
				t.Fatalf("AggregateAnnouncementReadUsers() 长度 = %d, want %d（got=%+v）", len(got), len(tc.want), got)
			}
			for i := range tc.want {
				if got[i] != tc.want[i] {
					t.Fatalf("第 %d 行 = %+v, want %+v（got=%+v）", i, got[i], tc.want[i], got)
				}
			}
		})
	}
}
