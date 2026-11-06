package service

import (
	"strings"
	"testing"

	"github.com/go-quick-code/go-admin-pro/internal/module/notify/model"
)

// TestValidType 锁定 §4.13 类型枚举：恰好四个合法值。
func TestValidType(t *testing.T) {
	for _, tp := range []string{
		model.TypeBpmTodo, model.TypeBpmResult, model.TypeAnnouncement, model.TypeMeetingReminder,
	} {
		if !model.ValidType(tp) {
			t.Fatalf("ValidType(%q) = false, want true", tp)
		}
	}
	for _, tp := range []string{"", "email", "sms", "BPM_TODO", "bpm_todo ", "todo"} {
		if model.ValidType(tp) {
			t.Fatalf("ValidType(%q) = true, want false", tp)
		}
	}
}

// TestValidateInput 表驱动锁定投递输入契约（见 ValidateInput 注释）。
func TestValidateInput(t *testing.T) {
	valid := Input{
		UserID:      7,
		Type:        model.TypeBpmTodo,
		Title:       "您有新的审批任务",
		Content:     "张三提交了请假申请",
		BizType:     "bpm_task",
		BizID:       9,
		Route:       "/bpm/task-detail",
		RouteParams: `{"taskId":9}`,
	}

	cases := []struct {
		name    string
		mutate  func(*Input)
		wantErr bool
	}{
		{"合法输入", func(*Input) {}, false},
		{"仅必填项", func(i *Input) { i.Content, i.BizType, i.BizID, i.Route, i.RouteParams = "", "", 0, "", "" }, false},
		{"userId 缺失", func(i *Input) { i.UserID = 0 }, true},
		{"userId 为负", func(i *Input) { i.UserID = -1 }, true},
		{"类型非法", func(i *Input) { i.Type = "email" }, true},
		{"类型为空", func(i *Input) { i.Type = "" }, true},
		{"标题空白", func(i *Input) { i.Title = "   " }, true},
		{"标题为空", func(i *Input) { i.Title = "" }, true},
		{"标题 128 字符", func(i *Input) { i.Title = strings.Repeat("告", 128) }, false},
		{"标题 129 字符", func(i *Input) { i.Title = strings.Repeat("告", 129) }, true},
		{"内容 500 字符", func(i *Input) { i.Content = strings.Repeat("摘", 500) }, false},
		{"内容 501 字符", func(i *Input) { i.Content = strings.Repeat("摘", 501) }, true},
		{"业务类型 65 字符", func(i *Input) { i.BizType = strings.Repeat("t", 65) }, true},
		{"路由 256 字符", func(i *Input) { i.Route = "/" + strings.Repeat("p", 255) }, true},
		{"跳转参数非法 JSON", func(i *Input) { i.RouteParams = "{oops" }, true},
		{"跳转参数非 JSON 对象", func(i *Input) { i.RouteParams = "[1,2]" }, true},
		{"跳转参数 null", func(i *Input) { i.RouteParams = "null" }, true},
		{"跳转参数 JSON 字符串", func(i *Input) { i.RouteParams = `"x"` }, true},
		{"跳转参数合法对象", func(i *Input) { i.RouteParams = `{"taskId":9,"tab":"todo"}` }, false},
		{"跳转参数空对象", func(i *Input) { i.RouteParams = `{}` }, false},
		{"跳转参数 513 字符", func(i *Input) {
			i.RouteParams = `{"k":"` + strings.Repeat("a", 506) + `"}`
			if len([]rune(i.RouteParams)) <= maxRouteParamsLen {
				t.Fatalf("用例自身无效：构造的 routeParams 长度 %d 未超过 %d", len([]rune(i.RouteParams)), maxRouteParamsLen)
			}
		}, true},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			in := valid
			tc.mutate(&in)
			err := ValidateInput(&in)
			if tc.wantErr && err == nil {
				t.Fatalf("ValidateInput() = nil, want error")
			}
			if !tc.wantErr && err != nil {
				t.Fatalf("ValidateInput() = %v, want nil", err)
			}
		})
	}
}

// TestValidateInputNil 空指针输入直接拒绝。
func TestValidateInputNil(t *testing.T) {
	if err := ValidateInput(nil); err == nil {
		t.Fatal("ValidateInput(nil) = nil, want error")
	}
}
