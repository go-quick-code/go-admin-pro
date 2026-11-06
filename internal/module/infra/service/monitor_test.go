package service

import (
	"testing"
	"time"
)

// TestMonitorWindowClampAndAlignment 校验 requests 聚合窗口的参数语义
// （issue #84，spec §4.10.2）：
//   - hours 非法（<=0 或 >168）时收敛到 [1,168]，0 取默认 24；
//   - start 对齐整点：10:30 发起的 24 小时窗口，往前落点为昨日 10:30，
//     截断到 10:00——分桶边界必须整点，否则跨桶数据会被切到两个桶里。
func TestMonitorWindowClampAndAlignment(t *testing.T) {
	loc, err := time.LoadLocation("Asia/Shanghai")
	if err != nil {
		t.Fatalf("加载时区: %v", err)
	}
	// 固定 10:30:15，避免依赖真实当前时间。
	now := time.Date(2026, 11, 7, 10, 30, 15, 0, loc)

	cases := []struct {
		name      string
		hours     int
		wantHours int
		wantStart time.Time
	}{
		{"默认24小时", 0, 24, time.Date(2026, 11, 6, 10, 0, 0, 0, loc)},
		{"整点对齐", 24, 24, time.Date(2026, 11, 6, 10, 0, 0, 0, loc)},
		{"负数取默认", -3, 24, time.Date(2026, 11, 6, 10, 0, 0, 0, loc)},
		{"上限168", 200, 168, time.Date(2026, 10, 31, 10, 0, 0, 0, loc)},
		{"下限1", 1, 1, time.Date(2026, 11, 7, 9, 0, 0, 0, loc)},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			start, end, got := monitorWindow(tc.hours, now, loc)
			if got != tc.wantHours {
				t.Fatalf("hours=%d 收敛为 %d，期望 %d", tc.hours, got, tc.wantHours)
			}
			if !start.Equal(tc.wantStart) {
				t.Fatalf("start = %s，期望 %s", start, tc.wantStart)
			}
			if !end.Equal(now) {
				t.Fatalf("end = %s，期望 %s", end, now)
			}
			if start.Minute() != 0 || start.Second() != 0 || start.Nanosecond() != 0 {
				t.Fatalf("start 未对齐整点: %s", start)
			}
			if start.Location() != loc {
				t.Fatalf("start 时区应为业务时区，实际 %s", start.Location())
			}
		})
	}
}

// TestMonitorHourBucketLabel 校验分桶标签：任意时刻落到其所在整点，
// 标签按业务时区格式化（YYYY-MM-DD HH:00）。
func TestMonitorHourBucketLabel(t *testing.T) {
	loc, err := time.LoadLocation("Asia/Shanghai")
	if err != nil {
		t.Fatalf("加载时区: %v", err)
	}
	cases := []struct {
		at   time.Time
		want string
	}{
		{time.Date(2026, 11, 7, 10, 30, 15, 0, loc), "2026-11-07 10:00"},
		{time.Date(2026, 11, 7, 0, 0, 0, 0, loc), "2026-11-07 00:00"},
		{time.Date(2026, 11, 7, 23, 59, 59, 0, loc), "2026-11-07 23:00"},
	}
	for _, tc := range cases {
		if got := monitorHourBucket(tc.at, loc); got != tc.want {
			t.Fatalf("%s 的分桶标签为 %q，期望 %q", tc.at, got, tc.want)
		}
	}
}
