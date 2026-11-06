//go:build integration

// 未注册 BusinessHandler 的业务类型不允许发起实例（issue #47 验收矩阵；
// spec #42 §4.4：发布与发起两侧都有注册 guard）。
//
// 发布侧 guard 由 definition_rules_http_integration_test.go 覆盖；本用例锁定
// 发起侧语义：历史数据中已存在「已发布但未注册处理器」的定义时，业务提交
// 必须得到可读 400，而不是创建无人处理的实例。
package bpm_test

import (
	"context"
	"strings"
	"testing"

	"github.com/go-quick-code/go-admin-pro/internal/module/bpm/model"
	bpmservice "github.com/go-quick-code/go-admin-pro/internal/module/bpm/service"
	"github.com/go-quick-code/go-admin-pro/internal/pkg/authn"
	"github.com/go-quick-code/go-admin-pro/internal/pkg/tenant"
	"github.com/go-quick-code/go-admin-pro/internal/pkg/xerr"
	"github.com/go-quick-code/go-admin-pro/internal/testutil"
)

func TestIntegrationRuntimeStartRequiresRegisteredHandler(t *testing.T) {
	env := testutil.NewIntegrationEnv(t)

	// 直接落一条已发布定义，模拟「定义先于 handler 注册存在」的历史数据
	// （正常发布路径会被发布 guard 拒绝，见上述发布侧用例）。
	ctx := tenant.WithTenantID(context.Background(), 1)
	def := &model.ProcessDefinition{
		TenantID:     1,
		Key:          "unregistered_start_flow",
		Version:      1,
		Name:         "未注册处理器流程",
		BusinessType: "unregistered_start_business",
		Nodes: model.NodeList{{
			Name: "审批", AssigneeType: model.AssigneeInitiator,
		}},
		Status: model.DefinitionStatusPublished,
	}
	if err := tenant.Skip(env.DB.WithContext(ctx)).Create(def).Error; err != nil {
		t.Fatalf("落已发布定义: %v", err)
	}

	initCtx := authn.With(ctx, authn.Principal{UserID: 1, TenantID: 1})
	_, err := env.App.BPM.StartInstance(initCtx, bpmservice.StartInstanceInput{
		DefinitionID: def.ID,
		BusinessID:   1,
		Title:        "未注册处理器发起",
	})
	if err == nil {
		t.Fatal("未注册业务处理器时发起应失败")
	}
	xe, ok := xerr.As(err)
	if !ok || xe.Status != 400 {
		t.Fatalf("未注册业务处理器发起应 400，实际 %v", err)
	}
	if !strings.Contains(xe.Msg, "未注册处理器") {
		t.Fatalf("错误文案应包含「未注册处理器」，实际 %q", xe.Msg)
	}
}
