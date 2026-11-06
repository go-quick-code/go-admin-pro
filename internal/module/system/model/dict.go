package model

import "time"

// DictType 是字典类型。
type DictType struct {
	BaseEntity
	TenantID int64  `gorm:"index;not null;default:1" json:"tenantId"`
	Name     string `gorm:"size:100;not null" json:"name"`
	Type     string `gorm:"size:100;not null" json:"type"`
	Status   int8   `gorm:"not null;default:0" json:"status"`
	Remark   string `gorm:"size:500" json:"remark"`
}

func (DictType) TableName() string { return "system_dict_type" }

// DictData 是字典数据。
type DictData struct {
	BaseEntity
	TenantID  int64  `gorm:"index;not null;default:1" json:"tenantId"`
	DictType  string `gorm:"size:100;index;not null" json:"dictType"`
	Label     string `gorm:"size:100;not null" json:"label"`
	Value     string `gorm:"size:100;not null" json:"value"`
	Sort      int    `gorm:"not null;default:0" json:"sort"`
	Status    int8   `gorm:"not null;default:0" json:"status"`
	ColorType string `gorm:"size:32" json:"colorType"`
	CSSClass  string `gorm:"size:100" json:"cssClass"`
	Remark    string `gorm:"size:500" json:"remark"`
}

func (DictData) TableName() string { return "system_dict_data" }

// Tenant 是租户。租户表本身不参与租户过滤。
type Tenant struct {
	BaseEntity
	Name          string     `gorm:"size:100;not null" json:"name"`
	ContactName   string     `gorm:"size:64" json:"contactName"`
	ContactMobile string     `gorm:"size:32" json:"contactMobile"`
	Status        int8       `gorm:"not null;default:0" json:"status"`
	PackageID     int64      `gorm:"index;not null;default:0" json:"packageId"`
	Domain        string     `gorm:"size:200" json:"domain"`
	ExpireTime    *time.Time `json:"expireTime"`
	AccountCount  int        `gorm:"not null;default:0" json:"accountCount"`
	Remark        string     `gorm:"size:500" json:"remark"`
}

func (Tenant) TableName() string { return "system_tenant" }

// LoginLog 是登录日志。
type LoginLog struct {
	BaseEntity
	TenantID  int64     `gorm:"index;not null;default:1" json:"tenantId"`
	UserID    int64     `gorm:"index" json:"userId"`
	Username  string    `gorm:"size:64" json:"username"`
	LoginIP   string    `gorm:"size:64" json:"loginIp"`
	UserAgent string    `gorm:"size:512" json:"userAgent"`
	LoginType int8      `gorm:"not null;default:1" json:"loginType"` // 1 账号密码 2 短信 3 社交
	Result    int8      `gorm:"not null;default:0" json:"result"`    // 0 成功 1 失败
	Message   string    `gorm:"size:500" json:"message"`
	LoginTime time.Time `json:"loginTime"`
}

func (LoginLog) TableName() string { return "system_login_log" }

// OperateLog 是操作日志。
type OperateLog struct {
	BaseEntity
	TenantID      int64     `gorm:"index;not null;default:1" json:"tenantId"`
	UserID        int64     `gorm:"index" json:"userId"`
	Username      string    `gorm:"size:64" json:"username"`
	Module        string    `gorm:"size:100" json:"module"`
	Name          string    `gorm:"size:100" json:"name"`
	Type          int8      `gorm:"not null;default:0" json:"type"` // 1 新增 2 修改 3 删除 4 查询 5 导出 6 导入
	RequestMethod string    `gorm:"size:16" json:"requestMethod"`
	RequestURL    string    `gorm:"size:500" json:"requestUrl"`
	RequestParams string    `gorm:"type:text" json:"requestParams"`
	Status        int8      `gorm:"not null;default:0" json:"status"` // 0 成功 1 失败
	ErrorMsg      string    `gorm:"type:text" json:"errorMsg"`
	OperateIP     string    `gorm:"size:64" json:"operateIp"`
	UserAgent     string    `gorm:"size:512" json:"userAgent"`
	Duration      int64     `json:"duration"` // 毫秒
	OperateTime   time.Time `json:"operateTime"`
}

func (OperateLog) TableName() string { return "system_operate_log" }
