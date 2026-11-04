package model

import (
	basemodel "github.com/lautung/go-admin-pro/internal/pkg/model"
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

// 数据权限范围。
const (
	DataScopeAll        = 1 // 全部数据
	DataScopeDeptAndSub = 2 // 本部门及以下
	DataScopeDept       = 3 // 本部门
	DataScopeSelf       = 4 // 仅本人
)
