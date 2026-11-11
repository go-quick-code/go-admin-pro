package repository

import (
	"context"

	"gorm.io/gorm"
)

// sequence.go 是 pay 单号序列发放（docs/pay-mvp-spec.md §4.9，issue #131）。
// 机制复制 erp_sequence（不 import erp，spec §2）：id 自增主键 +
// UNIQUE(tenant_id, biz_type, date_key)（发放锁行），事务内 INSERT ...
// ON DUPLICATE KEY UPDATE 原子递增后同事务回读。

// NextSequenceValue 在 (tenantID, bizType, dateKey) 上原子发放下一个序号。
//
// 并发安全（与 erp 同构）：单条 INSERT ... ON DUPLICATE KEY UPDATE 在唯一键
// 行锁内完成「首行插入或自增」，并发请求在同一行上串行化；随后在同一事务内
// 回读 last_value——本事务持有的行锁保证读到的就是本事务发放的值。
// 必须包在事务里：autocommit 下 upsert 与回读之间允许并发发放，会把同一序号
// 发给两个请求。租户值为显式入参：裸 SQL 不经租户插件。
func (r *Repository) NextSequenceValue(ctx context.Context, tenantID int64, bizType, dateKey string) (int64, error) {
	var value int64
	err := r.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if err := tx.Exec(
			"INSERT INTO `pay_sequence` (`tenant_id`, `biz_type`, `date_key`, `last_value`) VALUES (?, ?, ?, 1) "+
				"ON DUPLICATE KEY UPDATE `last_value` = `last_value` + 1",
			tenantID, bizType, dateKey,
		).Error; err != nil {
			return err
		}
		return tx.Raw(
			"SELECT `last_value` FROM `pay_sequence` WHERE `tenant_id` = ? AND `biz_type` = ? AND `date_key` = ?",
			tenantID, bizType, dateKey,
		).Scan(&value).Error
	})
	if err != nil {
		return 0, err
	}
	return value, nil
}
