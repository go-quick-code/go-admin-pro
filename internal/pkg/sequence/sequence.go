// Package sequence 提供序号发放原语：在 (tenant_id, biz_type, date_key) 计数行上
// 原子发放下一个序号。这是全库唯一一份发放实现（ADR 0022：并发不变量单点化），
// crm/erp/pay 三张序列表（crm_sequence / erp_sequence / pay_sequence）共用同一
// 机制，仅表名由调用方传入；日期键与编号格式化不是并发关键，留在各模块。
//
// 并发语义（权威说明，模块薄包装不再重复声明）：
//
//   - 发放形状是「upsert 原子递增 + 同事务回读」：单条 INSERT ... ON DUPLICATE
//     KEY UPDATE 在 (tenant_id, biz_type, date_key) 唯一键的计数行锁内完成
//     「首行插入或自增」，并发请求在同一行上串行化；随后在同一事务内回读
//     last_value——本事务持有的行锁保证读到的就是本事务发放的值，不会读入
//     并发方已发放的更大值。
//   - 必须包在调用方业务事务里：发放与单据落库同事务，任一步失败整体回滚，
//     不消耗序号、不留脏行；调用方事务持有的序列行锁要到业务事务结束才释放。
//     Next 内部保留一层 Transaction 作为 fail-closed 第二道防线：调用方误在
//     autocommit 句柄上调用时，upsert 与回读仍在内层事务中完成，绝不静默重号
//     （只是失去「回滚不消耗序号」的保证）；调用方传入事务句柄时 GORM 自动
//     改走 savepoint，行为不变。
//   - 锁序约定：调用方事务先取序列行锁、再写业务行，全库加锁顺序一致，
//     无死锁环。
//   - 租户值为显式入参：裸 SQL 不经租户插件，不吃 TenantTables 自动租户条件。
//   - 标识符统一加反引号：last_value 与 MySQL 8 窗口函数关键字 LAST_VALUE
//     同形，裸 SQL 的列清单里不加引号会被解析器拒绝（GORM 生成的语句自带
//     引号，不受影响）。
//
// 信任契约：table 必须是编译期字面量（crm_sequence / erp_sequence /
// pay_sequence），不得来自运行时输入；Next 入口按 ^[a-z0-9_]+$ 校验表名，
// 不合法在触碰 db 之前直接返回错误——这是 fail-closed 兜底（与内层事务的
// 第二道防线同一哲学），不给未来消费者留裸拼 SQL 的注入足枪，也不引入
// 表名类型抽象（ADR 0022）。
//
// 用法：各模块 Repository 的 NextSequenceValue 是本包的薄包装（签名不变、表名
// 各传各的），业务调用点一律经模块包装，不直调本包。
package sequence

import (
	"context"
	"fmt"
	"regexp"

	"gorm.io/gorm"
)

// tableRe 限制表名为小写字母/数字/下划线（与 dbschema 的表名白名单同形，
// 收紧为小写以匹配既有序列表命名）。
var tableRe = regexp.MustCompile(`^[a-z0-9_]+$`)

// validateTable 校验表名白名单：Next 的表名按包注释的信任契约必须是编译期
// 字面量，本函数是入口 fail-closed 兜底，不合法直接拒绝。
func validateTable(table string) error {
	if !tableRe.MatchString(table) {
		return fmt.Errorf("表名 %q 不合法：只允许小写字母、数字与下划线（^[a-z0-9_]+$），且必须是编译期字面量，不得来自运行时输入", table)
	}
	return nil
}

// Next 在 table 序列表的 (tenantID, bizType, dateKey) 计数行上原子发放下一个
// 序号。db 是调用方句柄（业务事务或其绑定物，见包注释的并发语义）。table
// 须为编译期字面量并通过入口校验，不合法在触碰 db 之前直接返回错误（信任
// 契约见包注释）。
func Next(ctx context.Context, db *gorm.DB, table string, tenantID int64, bizType, dateKey string) (int64, error) {
	if err := validateTable(table); err != nil {
		return 0, err
	}
	var value int64
	err := db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if err := tx.Exec(upsertSQL(table), tenantID, bizType, dateKey).Error; err != nil {
			return err
		}
		return tx.Raw(readSQL(table), tenantID, bizType, dateKey).Scan(&value).Error
	})
	if err != nil {
		return 0, err
	}
	return value, nil
}

// upsertSQL 构造发放 upsert 语句（纯函数）：三个 ? 按位置对应 tenant_id、
// biz_type、date_key（首行插入计 1）；标识符全部反引号（LAST_VALUE 同形）。
func upsertSQL(table string) string {
	return "INSERT INTO `" + table + "` (`tenant_id`, `biz_type`, `date_key`, `last_value`) VALUES (?, ?, ?, 1) " +
		"ON DUPLICATE KEY UPDATE `last_value` = `last_value` + 1"
}

// readSQL 构造同事务回读语句（纯函数）：三个 ? 按位置对应 tenant_id、biz_type、
// date_key，与 upsertSQL 的入参顺序一致。
func readSQL(table string) string {
	return "SELECT `last_value` FROM `" + table + "` WHERE `tenant_id` = ? AND `biz_type` = ? AND `date_key` = ?"
}
