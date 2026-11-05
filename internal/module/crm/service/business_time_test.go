package service

// 业务时区单测（PR #39 审查 P2，ADR 0006）：编号日期键、派生状态与公海回收的
// 「今天」必须按 app.timezone 解释，而不是服务器本地时区。这里用固定时区 +
// 固定时刻验证 businessNow 的换算：UTC 16:30 在 UTC+12 已是次日，若直接用
// time.Now() 的 Location（UTC）会得到前一天的日期键。

import (
	"testing"
	"time"
)

func TestBusinessNowUsesBusinessLocation(t *testing.T) {
	loc := time.FixedZone("UTC+12", 12*3600)
	// UTC 2026-11-05 16:30 = UTC+12 的 2026-11-06 04:30。
	fixed := time.Date(2026, 11, 5, 16, 30, 0, 0, time.UTC)

	svc := &Service{
		location: loc,
		clock:    func() time.Time { return fixed },
	}
	got := svc.businessNow()
	if got.Location() != loc {
		t.Fatalf("businessNow 的 Location = %v，期望业务时区 %v", got.Location(), loc)
	}
	if key := DateKey(got); key != "20261106" {
		t.Fatalf("业务时区日期键 = %q，期望 20261106（UTC 日期 %q 会落错一天）", key, DateKey(fixed))
	}
}

// TestBusinessNowFallsBackToLocalWithoutLocation 未注入业务时区时退回 time.Local
// （直接构造 Service 的测试/工具场景），与 businessLocation 的既有口径一致。
func TestBusinessNowFallsBackToLocalWithoutLocation(t *testing.T) {
	fixed := time.Date(2026, 11, 5, 1, 2, 3, 0, time.UTC)
	svc := &Service{clock: func() time.Time { return fixed }}
	got := svc.businessNow()
	if got.Location() != time.Local {
		t.Fatalf("未注入时区时 Location = %v，期望 time.Local", got.Location())
	}
	if !got.Equal(fixed) {
		t.Fatalf("businessNow = %v，期望同一时刻 %v", got, fixed)
	}
}
