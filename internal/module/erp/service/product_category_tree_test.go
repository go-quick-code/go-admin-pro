package service_test

import (
	"strings"
	"testing"

	"github.com/go-quick-code/go-admin-pro/internal/module/erp/model"
	"github.com/go-quick-code/go-admin-pro/internal/module/erp/service"
)

// 产品分类父校验纯函数单测（issue #88，spec §5：GET list 全量、前端组树——
// 后端只校验父合法性，无树组装职责）：自指/环/父不存在是纯逻辑，单测钉死行为；
// 落库路径由 HTTP 集成测试覆盖。

// erpCatRow 构造测试用分类行（ID 走赋值：BaseEntity.ID 是提升字段，不能进字面量）。
func erpCatRow(id, parentID int64) model.ProductCategory {
	row := model.ProductCategory{ParentID: parentID, Name: "c"}
	row.ID = id
	return row
}

func TestValidateProductCategoryParent(t *testing.T) {
	list := []model.ProductCategory{
		erpCatRow(1, 0), // 根
		erpCatRow(2, 1), // 1 的子
		erpCatRow(3, 2), // 2 的子（1 的孙）
	}
	tests := []struct {
		name     string
		parentID int64
		selfID   int64
		list     []model.ProductCategory
		wantErr  string
	}{
		{name: "挂根直接通过", parentID: 0, selfID: 0, list: list},
		{name: "合法父级", parentID: 1, selfID: 0, list: list},
		{name: "父不存在", parentID: 999, selfID: 0, list: list, wantErr: "上级分类不存在"},
		{name: "父是自己", parentID: 2, selfID: 2, list: list, wantErr: "上级分类不能是自己"},
		{name: "父是自己的子孙", parentID: 3, selfID: 2, list: list, wantErr: "上级分类不能是自己的子孙分类"},
		{
			// 脏数据：1↔2 互为父节点成环。校验不得死循环（guard 截断），
			// 且 selfID 不在环上时不误报。
			name:     "父链成环的脏数据由 guard 截断",
			parentID: 1, selfID: 3,
			list: []model.ProductCategory{erpCatRow(1, 2), erpCatRow(2, 1), erpCatRow(3, 2)},
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			err := service.ValidateProductCategoryParent(list, tc.parentID, tc.selfID)
			if tc.wantErr == "" {
				if err != nil {
					t.Fatalf("期望通过，实际 %v", err)
				}
				return
			}
			if err == nil || !strings.Contains(err.Error(), tc.wantErr) {
				t.Fatalf("期望错误含 %q，实际 %v", tc.wantErr, err)
			}
		})
	}
}
