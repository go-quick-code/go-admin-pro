package seed

import (
	_ "embed"
	"encoding/json"
	"fmt"
	"log/slog"

	sysmodel "github.com/go-quick-code/go-admin-pro/internal/module/system/model"
	"github.com/go-quick-code/go-admin-pro/internal/pkg/tenant"
	"gorm.io/gorm"
)

// areaSeedItem 是地区种子 JSON 的单条记录（data/area.json）。
// sort 由写入时按「同父级内的出现顺序」生成，JSON 不冗余携带。
type areaSeedItem struct {
	Code       string `json:"code"`
	ParentCode string `json:"parentCode"`
	Name       string `json:"name"`
	Level      int    `json:"level"`
}

// areaJSON 内嵌省市两级行政区划（issue #82，spec §4.8.2：种子写入省 + 市
// 两级，区县由平台导入接口补齐）。数据为 GB/T 2260 口径：34 个省级行政区
// + 各地级市/州/盟（直辖市下挂「市辖区」，重庆另挂「县」），共 377 行；
// 父节点保证先于子节点出现。仓库只内置代码数据，不含任何三方数据文件。
//
//go:embed data/area.json
var areaJSON []byte

// Areas 写入省市两级种子：表为空时全量写入，幂等（表非空直接跳过，
// 不做 upsert——重复执行不产生重复行，也不覆盖库内已有数据）。
// 由 Run 在初始化时调用；集成测试直接调用以验证幂等性。
func Areas(db *gorm.DB) error {
	var count int64
	if err := db.Model(&sysmodel.Area{}).Count(&count).Error; err != nil {
		return err
	}
	if count > 0 {
		return nil
	}

	var items []areaSeedItem
	if err := json.Unmarshal(areaJSON, &items); err != nil {
		return fmt.Errorf("解析地区种子数据: %w", err)
	}
	if len(items) == 0 {
		return fmt.Errorf("地区种子数据为空")
	}

	// 同父级内按 JSON 顺序生成 sort；JSON 已按行政区划代码升序且
	// 父节点先于子节点（构建期校验过），列表本身有序。
	sortByParent := make(map[string]int, len(items))
	rows := make([]sysmodel.Area, 0, len(items))
	for _, it := range items {
		sortByParent[it.ParentCode]++
		rows = append(rows, sysmodel.Area{
			Code:       it.Code,
			ParentCode: it.ParentCode,
			Name:       it.Name,
			Level:      int8(it.Level),
			Sort:       sortByParent[it.ParentCode],
			Status:     sysmodel.StatusEnable,
		})
	}
	// system_area 是平台级参考数据（未登记 TenantTables），这里仍按 seed
	// 包惯例显式 tenant.Skip——租户插件对未登记表是 no-op，写法保持一致。
	if err := tenant.Skip(db).Create(&rows).Error; err != nil {
		return fmt.Errorf("写入地区种子: %w", err)
	}
	slog.Info("seed areas created", "count", len(rows))
	return nil
}
