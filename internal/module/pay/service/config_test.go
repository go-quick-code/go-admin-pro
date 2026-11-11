package service

import "testing"

// 渠道凭证三段式纯函数（照 message_channel 先例，spec §3.1）：明文落库、
// 查询脱敏 ******、更新占位符合并。表驱动锁定脱敏边界与合并语义。
func TestMaskConfig(t *testing.T) {
	tests := []struct {
		name string
		raw  string
		want string
	}{
		{"空配置落 {} 照常展示", "", "{}"},
		{"非对象形态宁脱敏不回显", `[1,2]`, "{}"},
		{"密钥字段替换为占位符", `{"secretKey":"abc","apiKey":"k"}`, `{"apiKey":"******","secretKey":"******"}`},
		{"非密钥字段保留明文", `{"signName":"签名","mockPay":true}`, `{"mockPay":true,"signName":"签名"}`},
		{"空串密钥不脱敏", `{"secretKey":""}`, `{"secretKey":""}`},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := maskConfig(tt.raw); got != tt.want {
				t.Fatalf("maskConfig(%q) = %q，期望 %q", tt.raw, got, tt.want)
			}
		})
	}
}

func TestMergeSecretConfig(t *testing.T) {
	tests := []struct {
		name    string
		oldRaw  string
		newRaw  string
		want    string
		wantErr bool
	}{
		{
			name:   "提交占位符保留原密钥值",
			oldRaw: `{"secretKey":"abc"}`,
			newRaw: `{"secretKey":"******","signName":"新签名"}`,
			want:   `{"secretKey":"abc","signName":"新签名"}`,
		},
		{
			name:   "提交占位符但原值没有该键则删除（不把占位符当密钥落库）",
			oldRaw: `{"signName":"x"}`,
			newRaw: `{"secretKey":"******"}`,
			want:   `{}`,
		},
		{
			name:   "非占位符按提交值覆盖",
			oldRaw: `{"secretKey":"abc"}`,
			newRaw: `{"secretKey":"new"}`,
			want:   `{"secretKey":"new"}`,
		},
		{
			name:   "旧值异常不阻断更新",
			oldRaw: `not-json`,
			newRaw: `{"secretKey":"v"}`,
			want:   `{"secretKey":"v"}`,
		},
		{
			name:    "新值非法 JSON 报错",
			oldRaw:  `{"a":1}`,
			newRaw:  `bad`,
			wantErr: true,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := mergeSecretConfig(tt.oldRaw, tt.newRaw)
			if tt.wantErr {
				if err == nil {
					t.Fatalf("mergeSecretConfig(%q,%q) 期望报错，实际 nil", tt.oldRaw, tt.newRaw)
				}
				return
			}
			if err != nil {
				t.Fatalf("mergeSecretConfig(%q,%q): %v", tt.oldRaw, tt.newRaw, err)
			}
			if got != tt.want {
				t.Fatalf("mergeSecretConfig(%q,%q) = %q，期望 %q", tt.oldRaw, tt.newRaw, got, tt.want)
			}
		})
	}
}
