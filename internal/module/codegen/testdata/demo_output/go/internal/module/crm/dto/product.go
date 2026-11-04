package dto

import (
	"github.com/lautung/go-admin-pro/internal/pkg/money"
)

// ---------- 产品（本文件由 codegen 生成（issue #10 样板），契约与 crm 客户/联系人同款）----------
// 产品状态枚举：1 在售 / 2 下架。
// ProductCreateReq 新建产品。ownerUserId 缺省当前登录用户（负责人快照随写落库）。金额以十进制字符串提交（如 "1234.56"），换算不经过 float64（internal/pkg/money）。
type ProductCreateReq struct {
	Name        string      `json:"name" binding:"required,max=128"`
	Category    string      `json:"category" binding:"max=64"`
	Unit        string      `json:"unit" binding:"max=32"`
	Price       money.Money `json:"price" binding:"required"`
	Status      *int8       `json:"status"`
	Remark      string      `json:"remark" binding:"max=500"`
	OwnerUserID int64       `json:"ownerUserId"`
}

// ProductUpdateReq 修改产品。ownerUserId 传 0 表示保持现负责人不变。
type ProductUpdateReq struct {
	ID          int64       `json:"id" binding:"required"`
	Name        string      `json:"name" binding:"required,max=128"`
	Category    string      `json:"category" binding:"max=64"`
	Unit        string      `json:"unit" binding:"max=32"`
	Price       money.Money `json:"price" binding:"required"`
	Status      *int8       `json:"status"`
	Remark      string      `json:"remark" binding:"max=500"`
	OwnerUserID int64       `json:"ownerUserId"`
}

// ProductPageReq 产品分页筛选：枚举传非法值直接 400，绝不静默当成「无此筛选」。
type ProductPageReq struct {
	PageNo      int    `form:"pageNo"`
	PageSize    int    `form:"pageSize"`
	Name        string `form:"name"`
	Unit        string `form:"unit"`
	Status      *int8  `form:"status"`
	OwnerUserID int64  `form:"ownerUserId"`
	OwnerDeptID int64  `form:"ownerDeptId"`
}
