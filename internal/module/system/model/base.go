package model

import (
	basemodel "github.com/go-quick-code/go-admin-pro/internal/pkg/model"

	datascope "github.com/go-quick-code/go-admin-pro/internal/pkg/datascope"
)

// BaseEntity 是统一实体底座，定义见 internal/pkg/model。
type BaseEntity = basemodel.BaseEntity

// 状态常量：0 启用 / 1 停用，与芋道保持一致。
const (
	StatusEnable  = 0
	StatusDisable = 1
)

// 菜单类型。
const (
	MenuTypeDir    = 1
	MenuTypeMenu   = 2
	MenuTypeButton = 3
)

// 数据权限范围：枚举收口在 internal/pkg/datascope（行级谓词引擎，CRM 复用），
// 对齐芋道 1–5：1 全部 / 2 自定义部门 / 3 本部门 / 4 本部门及以下 / 5 仅本人。
const (
	DataScopeAll        = int8(datascope.ScopeAll)        // 全部数据
	DataScopeCustom     = int8(datascope.ScopeCustom)     // 自定义部门
	DataScopeDept       = int8(datascope.ScopeDept)       // 本部门
	DataScopeDeptAndSub = int8(datascope.ScopeDeptAndSub) // 本部门及以下
	DataScopeSelf       = int8(datascope.ScopeSelf)       // 仅本人
)
