package service

import (
	"testing"
	"time"
)

// TestNewJobSchedulerUsesConfiguredLocation 锁定时区口径（spec §4.10：
// 每日任务按应用时区 01:00 执行）。issue #64 code-review P1：此前调度器用
// cron.WithSeconds() 创建，cron 表达式按服务器 TZ 解释。
func TestNewJobSchedulerUsesConfiguredLocation(t *testing.T) {
	loc, err := time.LoadLocation("Asia/Shanghai")
	if err != nil {
		t.Fatalf("加载时区: %v", err)
	}
	sched := NewJobScheduler(nil, loc)
	if got := sched.cron.Location(); got != loc {
		t.Fatalf("调度器时区应为 %v，实际 %v", loc, got)
	}

	// 无 TZ= 前缀的 6 段表达式按调度器时区解释：01:00 的 Next 结果落在
	// Asia/Shanghai 的 01:00:00。
	schedule, err := cronParser.Parse("0 0 1 * * *")
	if err != nil {
		t.Fatalf("解析 cron: %v", err)
	}
	base := time.Date(2026, 11, 6, 12, 0, 0, 0, loc)
	next := schedule.Next(base.In(loc))
	if next.Hour() != 1 || next.Minute() != 0 || next.Second() != 0 {
		t.Fatalf("每日 01:00 的下次触发应落在 01:00:00，实际 %v", next)
	}
	if next.Location() == nil || next.Location().String() != "Asia/Shanghai" {
		t.Fatalf("Next 结果应保留业务时区，实际 %v", next.Location())
	}
}
