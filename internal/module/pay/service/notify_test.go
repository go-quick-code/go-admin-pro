package service

import (
	"errors"
	"strings"
	"testing"

	paymodel "github.com/go-quick-code/go-admin-pro/internal/module/pay/model"
)

// notifyRetryIntervalSeconds 是商户回调重试间隔的纯函数（spec §4.5，照芋道
// NOTIFY_FREQUENCY）：第 N 次失败后取第 N 个间隔 {15,15,30,180,1800,1800,
// 1800,1800,3600} 中的第 N 个；首次 + 8 重试共 9 次，第 9 次失败置 20 彻底失败
// （不再重试）。表驱动锁定三件事：逐跳间隔、9 次封顶、非法输入不 panic。
func TestNotifyRetryIntervalSeconds(t *testing.T) {
	tests := []struct {
		name     string
		failures int
		want     int // 0 表示不再重试
	}{
		{"第 1 次失败后 15s", 1, 15},
		{"第 2 次失败后 15s", 2, 15},
		{"第 3 次失败后 30s", 3, 30},
		{"第 4 次失败后 180s", 4, 180},
		{"第 5 次失败后 1800s", 5, 1800},
		{"第 6 次失败后 1800s", 6, 1800},
		{"第 7 次失败后 1800s", 7, 1800},
		{"第 8 次失败后 3600s", 8, 3600},
		{"第 9 次失败不再重试", 9, 0},
		{"第 10 次失败不再重试", 10, 0},
		{"零值失败输入不 panic", 0, 0},
		{"负值输入不 panic", -3, 0},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := notifyRetryIntervalSeconds(tt.failures); got != tt.want {
				t.Fatalf("第 %d 次失败的重试间隔 = %ds，期望 %ds", tt.failures, got, tt.want)
			}
		})
	}
}

// TestNotifyMaxAttempts 锁定 9 次总量的口径：首次 + 8 重试 = 9（spec §4.5），
// 间隔表长度与最大通知次数自洽（间隔表少一个元素：最后一次失败不再排期）。
func TestNotifyMaxAttempts(t *testing.T) {
	if paymodel.NotifyTaskMaxTimes != 9 {
		t.Fatalf("最大通知次数应为 9（首次 + 8 重试），实际 %d", paymodel.NotifyTaskMaxTimes)
	}
	if len(notifyRetryIntervals) != int(paymodel.NotifyTaskMaxTimes)-1 {
		t.Fatalf("间隔表应恰为 %d 个（第 9 次失败不再排期），实际 %d 个", paymodel.NotifyTaskMaxTimes-1, len(notifyRetryIntervals))
	}
}

// classifyNotifyResult 是回调响应语义的纯函数（spec §4.5）：
//   - 请求失败（超时/连接错误）→ 22；
//   - HTTP 非 2xx → 22；
//   - 2xx 且响应体 {"code":0} → 10 成功；
//   - 2xx 但业务码非 0（含非 JSON / 空体，无法确认成功）→ 21。
//
// 21/22 均继续重试（由扫描条件 status IN (0,21,22) 保证）。
func TestClassifyNotifyResult(t *testing.T) {
	tests := []struct {
		name       string
		statusCode int
		body       string
		execErr    error
		wantStatus int8 // 10 成功 / 21 请求成功但结果失败 / 22 请求失败
		wantHas    bool // summary 是否含关键词
		keyword    string
	}{
		{"2xx 业务码 0 → 成功", 200, `{"code":0,"msg":"success"}`, nil, 10, true, `{"code":0,"msg":"success"}`},
		{"2xx 业务码非 0 → 21", 200, `{"code":1001,"msg":"订单不存在"}`, nil, 21, true, "订单不存在"},
		{"2xx 非 JSON 体 → 21（无法确认成功）", 200, `ok`, nil, 21, true, "ok"},
		{"2xx 空体 → 21", 204, ``, nil, 21, false, ""},
		{"HTTP 500 → 22", 500, `internal error`, nil, 22, true, "500"},
		{"HTTP 404 → 22", 404, `not found`, nil, 22, true, "404"},
		{"超时错误 → 22", 0, ``, errors.New("context deadline exceeded"), 22, true, "context deadline exceeded"},
		{"连接拒绝 → 22", 0, ``, errors.New("connection refused"), 22, true, "connection refused"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			status, summary := classifyNotifyResult(tt.statusCode, []byte(tt.body), tt.execErr)
			if status != tt.wantStatus {
				t.Fatalf("响应分类状态 = %d，期望 %d", status, tt.wantStatus)
			}
			if tt.wantHas && !strings.Contains(summary, tt.keyword) {
				t.Fatalf("响应摘要 %q 应包含 %q", summary, tt.keyword)
			}
		})
	}
}

// TestClassifyNotifySummaryTruncated 响应摘要截断到 1024 rune（列 varchar(1024)，
// 按 rune 截断保证不撕裂多字节字符、必然可入库）。
func TestClassifyNotifySummaryTruncated(t *testing.T) {
	long := make([]byte, 0, 3000)
	for i := 0; i < 3000; i++ {
		long = append(long, 'x')
	}
	_, summary := classifyNotifyResult(200, long, nil)
	if len([]rune(summary)) != 1024 {
		t.Fatalf("响应摘要应截断到 1024 rune，实际 %d", len([]rune(summary)))
	}
	// 多字节字符截断后仍必须是合法 UTF-8（可安全入库）。
	cjk := make([]rune, 0, 2000)
	for i := 0; i < 2000; i++ {
		cjk = append(cjk, '回')
	}
	_, summary = classifyNotifyResult(200, []byte(string(cjk)), nil)
	if len([]rune(summary)) != 1024 {
		t.Fatalf("中文响应摘要应截断到 1024 rune，实际 %d", len([]rune(summary)))
	}
}

// notifySignature 是 Pay-Signature 的纯函数：HMAC-SHA256(rawBody, key) hex。
// 期望值取自独立来源（openssl dgst -sha256 -hmac），非实现自算，防止
// 实现与测试同源同错。
func TestNotifySignature(t *testing.T) {
	tests := []struct {
		name    string
		body    string
		key     string
		wantHex string
	}{
		{
			"短报文",
			"hello",
			"dev-pay-key",
			"44e62d931d0cc64358a6b583cb44401c5db7f3cfeee729aa0cba69dc1172abe3",
		},
		{
			"JSON 报文",
			`{"type":1,"merchantOrderId":"MO-1"}`,
			"dev-pay-key",
			"c6e80f51653a9a7f2300ab40ade28caf2d5240cf2f0350f4497b9862a66fe2f5",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := notifySignature([]byte(tt.body), tt.key); got != tt.wantHex {
				t.Fatalf("HMAC 签名 = %s，期望 %s", got, tt.wantHex)
			}
		})
	}
}

// verifyNotifySignature 是 demo 回调端点的验签纯函数：伪造签名/缺失签名/
// 密钥为空一律拒绝（fail-closed），正确签名通过；比较须恒时（防时序侧信道）。
func TestVerifyNotifySignature(t *testing.T) {
	body := []byte(`{"type":1,"merchantOrderId":"MO-1"}`)
	key := "dev-pay-key"
	valid := notifySignature(body, key)

	if !verifyNotifySignature(body, valid, key) {
		t.Fatal("正确签名应通过验签")
	}
	for _, tc := range []struct {
		name      string
		signature string
		key       string
	}{
		{"伪造签名", "deadbeef", key},
		{"空签名", "", key},
		{"换密钥后签名不匹配", notifySignature(body, "other-key"), key},
		{"密钥为空拒绝（fail-closed）", valid, ""},
		{"签名大小写敏感", "44E62D931D0CC64358A6B583CB44401C5DB7F3CFEEE729AA0CBA69DC1172ABE3", key},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if verifyNotifySignature(body, tc.signature, tc.key) {
				t.Fatalf("%s 不应通过验签", tc.name)
			}
		})
	}
	// 报文被篡改后签名失效。
	if verifyNotifySignature([]byte(`{"type":1,"merchantOrderId":"MO-2"}`), valid, key) {
		t.Fatal("报文被篡改后不应通过验签")
	}
}

// TestNotifyTaskStatuses 锁定回调任务/日志的状态数值与迁移注释一致
// （00037：0 等待 / 10 成功 / 20 彻底失败 / 21 请求成功但结果失败 / 22 请求失败）。
func TestNotifyTaskStatuses(t *testing.T) {
	cases := []struct {
		name string
		got  int8
		want int8
	}{
		{"等待", paymodel.NotifyTaskStatusWaiting, 0},
		{"成功", paymodel.NotifyTaskStatusSuccess, 10},
		{"彻底失败", paymodel.NotifyTaskStatusFail, 20},
		{"请求成功但结果失败", paymodel.NotifyTaskStatusResultFail, 21},
		{"请求失败", paymodel.NotifyTaskStatusRequestFail, 22},
	}
	for _, tc := range cases {
		if tc.got != tc.want {
			t.Fatalf("%s 状态数值 = %d，期望 %d", tc.name, tc.got, tc.want)
		}
	}
}
