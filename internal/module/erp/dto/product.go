package dto

import (
	"github.com/go-quick-code/go-admin-pro/internal/pkg/money"
	"github.com/go-quick-code/go-admin-pro/internal/pkg/qty"
)

// ProductCreateReq 新建产品。金额/数量以十进制字符串提交（如 "59.90"/"0.150"），
// 换算不经过 float64（internal/pkg/money、internal/pkg/qty）；参考价缺省 0.00，
// 保质期/重量未提交保持 NULL。
type ProductCreateReq struct {
	CategoryId    int64       `json:"categoryId" binding:"required"`
	UnitId        int64       `json:"unitId" binding:"required"`
	Name          string      `json:"name" binding:"required,max=128"`
	BarCode       string      `json:"barCode" binding:"max=64"`
	Status        *int8       `json:"status"`
	Standard      string      `json:"standard" binding:"max=128"`
	ExpiryDay     *int32      `json:"expiryDay"`
	Weight        *qty.Qty    `json:"weight"`
	PurchasePrice money.Money `json:"purchasePrice"`
	SalePrice     money.Money `json:"salePrice"`
	MinPrice      money.Money `json:"minPrice"`
	Remark        string      `json:"remark" binding:"max=500"`
}

// ProductUpdateReq 修改产品。
type ProductUpdateReq struct {
	ID            int64       `json:"id" binding:"required"`
	CategoryId    int64       `json:"categoryId" binding:"required"`
	UnitId        int64       `json:"unitId" binding:"required"`
	Name          string      `json:"name" binding:"required,max=128"`
	BarCode       string      `json:"barCode" binding:"max=64"`
	Status        *int8       `json:"status"`
	Standard      string      `json:"standard" binding:"max=128"`
	ExpiryDay     *int32      `json:"expiryDay"`
	Weight        *qty.Qty    `json:"weight"`
	PurchasePrice money.Money `json:"purchasePrice"`
	SalePrice     money.Money `json:"salePrice"`
	MinPrice      money.Money `json:"minPrice"`
	Remark        string      `json:"remark" binding:"max=500"`
}

// ProductPageReq 产品分页筛选：枚举传非法值直接 400，绝不静默当成「无此筛选」。
type ProductPageReq struct {
	PageNo     int    `form:"pageNo"`
	PageSize   int    `form:"pageSize"`
	Name       string `form:"name"`
	BarCode    string `form:"barCode"`
	CategoryId *int64 `form:"categoryId"`
	UnitId     *int64 `form:"unitId"`
	Status     *int8  `form:"status"`
}
