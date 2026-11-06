package service

import (
	"testing"
	"time"
)

// 月度配额窗口（issue #75，spec §4.2.4）：当月边界按应用时区计算，
// [当月 1 日 00:00, 下月 1 日 00:00) 半开区间。期望值全部为手算字面量：
// 同一时刻在 Asia/Shanghai（UTC+8）与 UTC 可能属于不同月份，这是
// 「按应用时区而非服务器本地时区」划分的直接证据。
func TestMonthWindowByBusinessTimezone(t *testing.T) {
	shanghai, err := time.LoadLocation("Asia/Shanghai")
	if err != nil {
		t.Fatalf("加载 Asia/Shanghai: %v", err)
	}
	utc := time.UTC

	nov1Shanghai := time.Date(2026, 11, 1, 0, 0, 0, 0, shanghai)
	dec1Shanghai := time.Date(2026, 12, 1, 0, 0, 0, 0, shanghai)
	oct1UTC := time.Date(2026, 10, 1, 0, 0, 0, 0, utc)
	nov1UTC := time.Date(2026, 11, 1, 0, 0, 0, 0, utc)
	dec1UTC := time.Date(2026, 12, 1, 0, 0, 0, 0, utc)

	for _, tc := range []struct {
		name      string
		now       time.Time
		loc       *time.Location
		wantStart time.Time
		wantEnd   time.Time
	}{
		{
			name:      "北京时刻落在 11 月",
			now:       time.Date(2026, 11, 7, 1, 30, 0, 0, shanghai),
			loc:       shanghai,
			wantStart: nov1Shanghai,
			wantEnd:   dec1Shanghai,
		},
		{
			name:      "同一时刻按 UTC 也在 11 月（窗口随 loc 平移）",
			now:       time.Date(2026, 11, 7, 1, 30, 0, 0, shanghai),
			loc:       utc,
			wantStart: nov1UTC,
			wantEnd:   dec1UTC,
		},
		{
			name:      "北京 11 月 1 日 02:00 在 UTC 是 10 月 31 日 18:00：按北京时区归 11 月",
			now:       time.Date(2026, 11, 1, 2, 0, 0, 0, shanghai),
			loc:       shanghai,
			wantStart: nov1Shanghai,
			wantEnd:   dec1Shanghai,
		},
		{
			name:      "同一时刻按 UTC 归 10 月（跨月证据）",
			now:       time.Date(2026, 11, 1, 2, 0, 0, 0, shanghai),
			loc:       utc,
			wantStart: oct1UTC,
			wantEnd:   nov1UTC,
		},
		{
			name:      "月末最后一秒仍属当月（下界含、上界不含）",
			now:       time.Date(2026, 11, 30, 23, 59, 59, int(999*time.Millisecond), shanghai),
			loc:       shanghai,
			wantStart: nov1Shanghai,
			wantEnd:   dec1Shanghai,
		},
		{
			name:      "下月 1 日 00:00 整点即属下月",
			now:       time.Date(2026, 12, 1, 0, 0, 0, 0, shanghai),
			loc:       shanghai,
			wantStart: dec1Shanghai,
			wantEnd:   time.Date(2027, 1, 1, 0, 0, 0, 0, shanghai),
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			start, end := monthWindow(tc.now, tc.loc)
			if !start.Equal(tc.wantStart) || !end.Equal(tc.wantEnd) {
				t.Fatalf("monthWindow(%s, %s) = [%s, %s)，期望 [%s, %s)",
					tc.now, tc.loc, start, end, tc.wantStart, tc.wantEnd)
			}
		})
	}
}
