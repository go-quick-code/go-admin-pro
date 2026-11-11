package service

import "context"

// app_ref.go 是应用删除引用检查扩展点（docs/pay-mvp-spec.md §4.10）：
// 应用有订单/退款/转账引用禁删（409）。本票（#131）先立骨架——交易单据在
// #133/#134+ 落地，届时各自实现 AppRefChecker 并在 internal/app 装配处以
// RefCheckerChain 组合注册（同 crm/erp 先例）。未注册时应用删除只查渠道绑定。

// AppRefChecker 是应用删除引用检查的扩展点：报告应用是否被业务数据引用。
type AppRefChecker interface {
	// AppHasReference 报告 appID 是否有业务引用；ref 为引用说明（进 409 文案）。
	AppHasReference(ctx context.Context, appID int64) (referenced bool, ref string, err error)
}

// RefCheckerChain 依次调用引用检查，首个命中即拒绝（同 crm/erp 的 Chain 形态）。
type RefCheckerChain []AppRefChecker

// AppHasReference 实现 AppRefChecker：任一成员命中即返回。
func (chain RefCheckerChain) AppHasReference(ctx context.Context, appID int64) (bool, string, error) {
	for _, checker := range chain {
		referenced, ref, err := checker.AppHasReference(ctx, appID)
		if err != nil {
			return false, "", err
		}
		if referenced {
			return true, ref, nil
		}
	}
	return false, "", nil
}
