package repository

import (
	"gorm.io/gorm"
)

// scope.go 承载 ERP 各主数据列表共用的数据范围谓词（owner = 创建人，spec §4.10；
// datascope.Filter → owner_user_id/owner_dept_id 列的翻译，与 crm customerQuery 同构）。
//
// 语义（service 层 applyScope 翻译 datascope.Filter 后传入）：
//   - OwnerDeptIDs == nil 且 SelfOwnerUserID == 0：不加范围条件（数据范围「全部」）；
//   - OwnerDeptIDs 非 nil：部门类范围 IN 可见部门（OR 本人）；
//   - OwnerDeptIDs 为空切片：无可见部门（fail-closed），仅本人可见或谓词恒假。
func applyOwnerScope(q *gorm.DB, ownerDeptIDs []int64, selfOwnerUserID int64) *gorm.DB {
	switch {
	case len(ownerDeptIDs) > 0 && selfOwnerUserID > 0:
		return q.Where("owner_dept_id IN ? OR owner_user_id = ?", ownerDeptIDs, selfOwnerUserID)
	case len(ownerDeptIDs) > 0:
		return q.Where("owner_dept_id IN ?", ownerDeptIDs)
	case ownerDeptIDs != nil && selfOwnerUserID > 0:
		return q.Where("owner_user_id = ?", selfOwnerUserID)
	case ownerDeptIDs != nil:
		// 空切片且不含本人：谓词恒假，绝不回退全部。
		return q.Where("1 = 0")
	case selfOwnerUserID > 0:
		return q.Where("owner_user_id = ?", selfOwnerUserID)
	}
	return q
}
