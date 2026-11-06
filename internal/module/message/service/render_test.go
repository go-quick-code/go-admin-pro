package service

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"github.com/go-quick-code/go-admin-pro/internal/module/message/model"
)

// TestValidateTemplateParams 覆盖参数名形态与重复声明。
func TestValidateTemplateParams(t *testing.T) {
	cases := []struct {
		name    string
		params  []string
		wantErr bool
	}{
		{name: "空声明", params: nil},
		{name: "合法", params: []string{"name", "mobile", "_x1"}},
		{name: "数字开头", params: []string{"1name"}, wantErr: true},
		{name: "含空格", params: []string{"na me"}, wantErr: true},
		{name: "空参数名", params: []string{""}, wantErr: true},
		{name: "重复", params: []string{"name", "name"}, wantErr: true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := validateTemplateParams(tc.params)
			if tc.wantErr {
				if err == nil {
					t.Fatalf("期望报错，实际成功（%v）", got)
				}
				return
			}
			if err != nil {
				t.Fatalf("不期望报错：%v", err)
			}
			if len(got) != len(tc.params) {
				t.Fatalf("返回值长度 %d，期望 %d", len(got), len(tc.params))
			}
		})
	}
}

// TestValidateTemplateContent 覆盖占位符声明校验：未声明与非法写法都拒绝。
func TestValidateTemplateContent(t *testing.T) {
	if err := validateTemplateContent([]string{"name"}, "您好 {{name}}"); err != nil {
		t.Fatalf("已声明占位符不应报错：%v", err)
	}
	if err := validateTemplateContent(nil, "您好 {{name}}"); err == nil {
		t.Fatal("未声明占位符应报错")
	} else if !strings.Contains(err.Error(), "未声明的参数") {
		t.Fatalf("错误文案不符：%v", err)
	}
	if err := validateTemplateContent([]string{"name"}, "您好 {{ name }}"); err == nil {
		t.Fatal("带空格的占位符应报错（只支持 {{name}}）")
	}
	if err := validateTemplateContent([]string{"name"}, "您好 {{name}}，{{name}}"); err != nil {
		t.Fatalf("重复占位符不应报错：%v", err)
	}
}

// TestValidateSendParams 覆盖参数与模板声明完全一致（缺失/多余 400）。
func TestValidateSendParams(t *testing.T) {
	declared := []string{"name", "mobile"}
	if err := validateSendParams(declared, map[string]string{"name": "张三", "mobile": "13800000000"}); err != nil {
		t.Fatalf("参数完整不应报错：%v", err)
	}
	err := validateSendParams(declared, map[string]string{"name": "张三"})
	if err == nil || !strings.Contains(err.Error(), "缺少模板参数：mobile") {
		t.Fatalf("缺少参数应报错且指出 mobile，实际 %v", err)
	}
	err = validateSendParams(declared, map[string]string{"name": "张三", "mobile": "1", "extra": "x"})
	if err == nil || !strings.Contains(err.Error(), "多余的模板参数：extra") {
		t.Fatalf("多余参数应报错且指出 extra，实际 %v", err)
	}
	if err := validateSendParams(nil, nil); err != nil {
		t.Fatalf("无参数模板传空 map 不应报错：%v", err)
	}
	if err := validateSendParams(nil, map[string]string{"name": "x"}); err == nil {
		t.Fatal("无参数模板传参数应报错")
	}
}

// TestRenderTemplate 覆盖 {{name}} 替换与未解析占位的防御。
func TestRenderTemplate(t *testing.T) {
	got, err := renderTemplate("您好 {{name}}，您的验证码是 {{code}}。", map[string]string{
		"name": "张三", "code": "1234",
	})
	if err != nil {
		t.Fatalf("渲染失败：%v", err)
	}
	if got != "您好 张三，您的验证码是 1234。" {
		t.Fatalf("渲染结果不符：%q", got)
	}

	// 参数值中的 $ 与反斜杠不能被当成正则替换语法。
	got, err = renderTemplate("金额 {{amount}}", map[string]string{"amount": "$1\\2"})
	if err != nil || got != "金额 $1\\2" {
		t.Fatalf("特殊字符替换异常：%q, %v", got, err)
	}

	if _, err := renderTemplate("您好 {{name}}", nil); err == nil {
		t.Fatal("缺少参数时应报错")
	}
}

// TestValidateRecipient 覆盖手机号与邮箱的基础格式校验（spec §4.3.4）。
func TestValidateRecipient(t *testing.T) {
	smsCases := []struct {
		recipient string
		wantErr   bool
	}{
		{"13800000000", false},
		{"19912345678", false},
		{"12800000000", true},
		{"1380000000", true},
		{"138000000000", true},
		{"+8613800000000", true},
		{"", true},
	}
	for _, tc := range smsCases {
		if err := validateRecipient(model.ChannelTypeSMS, tc.recipient); (err != nil) != tc.wantErr {
			t.Fatalf("短信收件人 %q：err=%v，期望错误=%v", tc.recipient, err, tc.wantErr)
		}
	}

	mailCases := []struct {
		recipient string
		wantErr   bool
	}{
		{"user@example.com", false},
		{"user.name+tag@sub.example.com", false},
		{"not-an-email", true},
		{"Name <user@example.com>", true},
		{"user@@example.com", true},
		{"", true},
	}
	for _, tc := range mailCases {
		if err := validateRecipient(model.ChannelTypeMail, tc.recipient); (err != nil) != tc.wantErr {
			t.Fatalf("邮箱收件人 %q：err=%v，期望错误=%v", tc.recipient, err, tc.wantErr)
		}
	}

	if err := validateRecipient("wechat", "x"); err == nil {
		t.Fatal("未知渠道类型应报错")
	}
}

// TestMaskConfig 覆盖密钥脱敏：只替换非空字符串值，其他字段与嵌套对象保持原样。
func TestMaskConfig(t *testing.T) {
	raw := `{"username":"u","password":"p","secret":"s","accessKeySecret":"a","nested":{"password":"keep"},"port":465}`
	masked := maskConfig(raw)

	var obj map[string]any
	if err := json.Unmarshal([]byte(masked), &obj); err != nil {
		t.Fatalf("脱敏结果不是合法 JSON：%v（%s）", err, masked)
	}
	for _, key := range []string{"password", "secret", "accessKeySecret"} {
		if obj[key] != MaskedSecret {
			t.Fatalf("%s 应脱敏为 %s，实际 %v", key, MaskedSecret, obj[key])
		}
	}
	if obj["username"] != "u" || obj["port"] != float64(465) {
		t.Fatalf("非密钥字段不应被修改：%v", obj)
	}
	nested, ok := obj["nested"].(map[string]any)
	if !ok || nested["password"] != "keep" {
		t.Fatalf("嵌套对象不做顶层脱敏：%v", obj["nested"])
	}

	if got := maskConfig(""); got != "{}" {
		t.Fatalf("空配置脱敏应为 {}，实际 %s", got)
	}
	if got := maskConfig("not-json"); got != "{}" {
		t.Fatalf("非法配置应降级为 {}，实际 %s", got)
	}
	if got := maskConfig(`["a"]`); got != "{}" {
		t.Fatalf("非对象配置应降级为 {}，实际 %s", got)
	}
	if got := maskConfig(`{"password":123}`); strings.Contains(got, MaskedSecret) {
		t.Fatalf("非字符串密钥不做脱敏：%s", got)
	}
}

// TestMergeSecretConfig 覆盖更新时的 ****** 保留语义（spec §4.3.6）。
func TestMergeSecretConfig(t *testing.T) {
	oldRaw := `{"host":"smtp.old","password":"old-pass","secret":"old-secret"}`
	newRaw := `{"host":"smtp.new","password":"******","secret":"******"}`
	merged, err := mergeSecretConfig(oldRaw, newRaw)
	if err != nil {
		t.Fatalf("合并失败：%v", err)
	}
	var obj map[string]any
	if err := json.Unmarshal([]byte(merged), &obj); err != nil {
		t.Fatalf("合并结果非法：%v", err)
	}
	if obj["password"] != "old-pass" || obj["secret"] != "old-secret" {
		t.Fatalf("****** 应保留原密钥：%v", obj)
	}
	if obj["host"] != "smtp.new" {
		t.Fatalf("非密钥字段应按提交值覆盖：%v", obj)
	}

	// 提交真实新值：覆盖旧值。
	merged, err = mergeSecretConfig(oldRaw, `{"password":"new-pass"}`)
	if err != nil {
		t.Fatalf("合并失败：%v", err)
	}
	_ = json.Unmarshal([]byte(merged), &obj)
	if obj["password"] != "new-pass" {
		t.Fatalf("新密钥应覆盖旧值：%v", obj)
	}

	// 旧配置没有该键时，****** 不应被当成密钥落库。
	merged, err = mergeSecretConfig(`{"host":"h"}`, `{"accessKeySecret":"******"}`)
	if err != nil {
		t.Fatalf("合并失败：%v", err)
	}
	if strings.Contains(merged, MaskedSecret) {
		t.Fatalf("旧配置缺少密钥时应删除占位符：%s", merged)
	}

	if _, err := mergeSecretConfig(oldRaw, "not-json"); err == nil {
		t.Fatal("非法新配置应报错")
	}
}

// TestRegistryUnknownProviderFailClosed 覆盖未知 provider 的 fail-closed 行为。
func TestRegistryUnknownProviderFailClosed(t *testing.T) {
	registry := NewRegistry()
	if _, err := registry.Lookup("smtp"); !errors.Is(err, ErrUnknownProvider) {
		t.Fatalf("未注册 provider 应返回 ErrUnknownProvider，实际 %v", err)
	}

	registry.Register(NewLogSender())
	sender, err := registry.Lookup(model.ProviderLog)
	if err != nil {
		t.Fatalf("log provider 应可查找：%v", err)
	}
	if sender.Provider() != model.ProviderLog {
		t.Fatalf("provider 键不符：%s", sender.Provider())
	}

	// nil / 空 provider 的注册被忽略，不会污染注册表。
	registry.Register(nil)
	registry.Register(emptyProvider{})
	if _, err := registry.Lookup(""); !errors.Is(err, ErrUnknownProvider) {
		t.Fatalf("空 provider 不应被注册，实际 %v", err)
	}
}

type emptyProvider struct{}

func (emptyProvider) Provider() string { return "" }
func (emptyProvider) Send(context.Context, SendRequest) (SendResult, error) {
	return SendResult{}, nil
}

// TestLogSender 覆盖内置 log provider 的成功返回。
func TestLogSender(t *testing.T) {
	sender := NewLogSender()
	result, err := sender.Send(context.Background(), SendRequest{
		Channel:      model.Channel{ChannelType: model.ChannelTypeSMS, Provider: model.ProviderLog},
		TemplateCode: "welcome",
		Recipient:    "13800000000",
		Content:      "hello",
	})
	if err != nil {
		t.Fatalf("log provider 不应失败：%v", err)
	}
	if !strings.HasPrefix(result.ProviderMessageID, "log-") {
		t.Fatalf("provider message id 应以 log- 开头：%q", result.ProviderMessageID)
	}
}
