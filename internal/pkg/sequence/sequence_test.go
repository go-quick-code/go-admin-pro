package sequence

import (
	"strings"
	"testing"
)

// 本文件钉住发放 SQL 文本的安全契约面（spec #141 Testing Decisions：对本原语
// 而言 SQL 文本即安全契约）：
//   - 反引号转义：last_value 与 MySQL 8 窗口函数关键字 LAST_VALUE 同形，裸 SQL
//     的列清单里不加引号会被解析器拒绝；
//   - 参数顺序：upsert 的三个 ? 按位置对应 tenant_id、biz_type、date_key（首行
//     插入计 1），回读的三个 ? 顺序一致。
//
// 期望字面量取自重构前三份手写实现（crm/erp/pay 逐字节相同、仅表名不同），
// 是独立于本包代码的已知良好来源；重构后必须与它们逐字节等价。
// 并发不变量不在此层测：主缝是 pay 序列并发集成测试（打真实 MySQL）。

// TestUpsertSQL 发放 upsert 与三份既有实现逐字节等价（含 VALUES (?, ?, ?, 1) 的
// 参数占位顺序与字面量 1 的位置）。
func TestUpsertSQL(t *testing.T) {
	cases := []struct{ table, want string }{
		{table: "crm_sequence", want: "INSERT INTO `crm_sequence` (`tenant_id`, `biz_type`, `date_key`, `last_value`) VALUES (?, ?, ?, 1) " +
			"ON DUPLICATE KEY UPDATE `last_value` = `last_value` + 1"},
		{table: "erp_sequence", want: "INSERT INTO `erp_sequence` (`tenant_id`, `biz_type`, `date_key`, `last_value`) VALUES (?, ?, ?, 1) " +
			"ON DUPLICATE KEY UPDATE `last_value` = `last_value` + 1"},
		{table: "pay_sequence", want: "INSERT INTO `pay_sequence` (`tenant_id`, `biz_type`, `date_key`, `last_value`) VALUES (?, ?, ?, 1) " +
			"ON DUPLICATE KEY UPDATE `last_value` = `last_value` + 1"},
	}
	for _, tc := range cases {
		if got := upsertSQL(tc.table); got != tc.want {
			t.Errorf("upsertSQL(%q) 与既有实现不等价：\n got  %s\n want %s", tc.table, got, tc.want)
		}
	}
}

// TestReadSQL 同事务回读与三份既有实现逐字节等价（WHERE 三个条件的 ? 顺序即
// tenant_id、biz_type、date_key 的入参顺序，与 upsertSQL 一致）。
func TestReadSQL(t *testing.T) {
	cases := []struct{ table, want string }{
		{table: "crm_sequence", want: "SELECT `last_value` FROM `crm_sequence` WHERE `tenant_id` = ? AND `biz_type` = ? AND `date_key` = ?"},
		{table: "erp_sequence", want: "SELECT `last_value` FROM `erp_sequence` WHERE `tenant_id` = ? AND `biz_type` = ? AND `date_key` = ?"},
		{table: "pay_sequence", want: "SELECT `last_value` FROM `pay_sequence` WHERE `tenant_id` = ? AND `biz_type` = ? AND `date_key` = ?"},
	}
	for _, tc := range cases {
		if got := readSQL(tc.table); got != tc.want {
			t.Errorf("readSQL(%q) 与既有实现不等价：\n got  %s\n want %s", tc.table, got, tc.want)
		}
	}
}

// TestSQLIdentifiersBackquoted 反引号转义契约：tenant_id / biz_type / date_key /
// last_value / 表名的每一处出现都必须带反引号——字段名 last_value 与 MySQL 8
// 窗口函数 LAST_VALUE 同形，漏一处转义裸 SQL 即被解析器拒绝。
func TestSQLIdentifiersBackquoted(t *testing.T) {
	for _, sql := range []string{upsertSQL("crm_sequence"), readSQL("pay_sequence")} {
		for _, ident := range []string{"tenant_id", "biz_type", "date_key", "last_value"} {
			bare := strings.Count(sql, ident)
			quoted := strings.Count(sql, "`"+ident+"`")
			if bare != quoted {
				t.Errorf("%s：标识符 %s 有 %d 处出现、仅 %d 处带反引号（last_value 与 LAST_VALUE 同形，必须全部转义）",
					sql, ident, bare, quoted)
			}
		}
		if !strings.Contains(sql, "`crm_sequence`") && !strings.Contains(sql, "`pay_sequence`") {
			t.Errorf("%s：表名必须以反引号内插", sql)
		}
	}
}
