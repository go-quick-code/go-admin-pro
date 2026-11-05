// scope.go 承载 datascope.Filter 到 CRM owner_* 谓词的共享翻译。
package service

import "github.com/lautung/go-admin-pro/internal/pkg/datascope"

// applyCustomerScope 把 datascope.Filter 翻译成父客户 owner_* 谓词
// （与 ListCustomers 的翻译一致，供联系人/跟进/回款等 join 路径复用）。
func applyCustomerScope(scope datascope.Filter, ownerDeptIDs *[]int64, selfOwnerUserID *int64) {
	if scope.All {
		return
	}
	if scope.IncludeSelf {
		*selfOwnerUserID = scope.UserID
	}
	switch {
	case len(scope.DeptIDs) > 0:
		*ownerDeptIDs = scope.DeptIDs
	case !scope.IncludeSelf:
		// 无可见部门且不含本人：谓词恒假（fail-closed，绝不回退全部）。
		*ownerDeptIDs = []int64{}
	}
}
