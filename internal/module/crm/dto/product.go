package dto

import (
	"github.com/lautung/go-admin-pro/internal/pkg/money"
)

// ---------- 产品（本文件由 codegen 生成（issue #10 样板），契约与 crm 客户/联系人同款）----------
// 产品状态枚举：0 下架 / 1 上架。
// ProductCreateReq 新建产品。金额以十进制字符串提交（如 "1234.56"），换算不经过 float64（internal/pkg/money）。
type ProductCreateReq struct {
	CategoryId  int64       `json:"categoryId" binding:"required"`
	Name        string      `json:"name" binding:"required,max=128"`
	Code        string      `json:"code" binding:"required,max=64"`
	Unit        string      `json:"unit" binding:"max=32"`
	Price       money.Money `json:"price" binding:"required"`
	Status      *int8       `json:"status"`
	Description string      `json:"description" binding:"max=500"`
}

// ProductUpdateReq 修改产品。
type ProductUpdateReq struct {
	ID          int64       `json:"id" binding:"required"`
	CategoryId  int64       `json:"categoryId" binding:"required"`
	Name        string      `json:"name" binding:"required,max=128"`
	Code        string      `json:"code" binding:"required,max=64"`
	Unit        string      `json:"unit" binding:"max=32"`
	Price       money.Money `json:"price" binding:"required"`
	Status      *int8       `json:"status"`
	Description string      `json:"description" binding:"max=500"`
}

// ProductPageReq 产品分页筛选：枚举传非法值直接 400，绝不静默当成「无此筛选」。
// categoryId 由 #11 手工补：codegen 不生成 int64 筛选字段（分类树选择器联动）。
type ProductPageReq struct {
	PageNo     int    `form:"pageNo"`
	PageSize   int    `form:"pageSize"`
	Name       string `form:"name"`
	Code       string `form:"code"`
	Unit       string `form:"unit"`
	Status     *int8  `form:"status"`
	CategoryId *int64 `form:"categoryId"`
}
