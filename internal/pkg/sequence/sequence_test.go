package sequence

import (
	"context"
	"testing"
)

// 本文件钉住发放 SQL 文本的安全契约面（spec #141 Testing Decisions：对本原语
// 而言 SQL 文本即安全契约）：
//   - 反引号转义：last_value 与 MySQL 8 窗口函数关键字 LAST_VALUE 同形，裸 SQL
//     的列清单里不加引号会被解析器拒绝；
//   - 参数顺序：upsert 的三个 ? 按位置对应 tenant_id、biz_type、date_key（首行
//     插入计 1），回读的三个 ? 顺序一致；
//   - 表名白名单：Next 入口按 ^[a-z0-9_]+$ 校验（fail-closed 兜底，信任契约见
//     包注释），三张真实表名必须放行、注入形一律拒绝。
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

// TestValidateTable 表名白名单契约（Next 入口 guard 的纯函数部分）：只放行
// `^[a-z0-9_]+$`——三张真实序列表必须通过；反引号、点号、大写、空串与注入形
// 一律拒绝（表名虽约定为编译期字面量，入口仍 fail-closed，不给未来消费者留
// 注入足枪）。
func TestValidateTable(t *testing.T) {
	for _, table := range []string{"crm_sequence", "erp_sequence", "pay_sequence"} {
		if err := validateTable(table); err != nil {
			t.Errorf("validateTable(%q) 应放行真实序列表，却报错：%v", table, err)
		}
	}
	for _, table := range []string{"crm`_sequence", "crm.sequence", "CRM_SEQUENCE", "", "x`); DROP"} {
		if err := validateTable(table); err == nil {
			t.Errorf("validateTable(%q) 应拒绝，却放行了", table)
		}
	}
}

// TestNextRejectsInvalidTableBeforeDB 经公开入口 Next 钉住 guard 的接线：表名
// 不合法必须在触碰 db 之前返回错误（nil 句柄即安全——guard 失效时本测试以
// panic 显形），而不是等内层事务拼出 SQL 才失败。
func TestNextRejectsInvalidTableBeforeDB(t *testing.T) {
	const injection = "x`); DROP"
	_, err := Next(context.Background(), nil, injection, 1, "biz", "20260101")
	if err == nil {
		t.Fatalf("Next 对非法表名 %q 必须报错（在触碰 db 之前）", injection)
	}
}
