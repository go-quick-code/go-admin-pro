package testutil

import (
	"fmt"
	"strings"
)

// TestDatabaseName 是集成测试专用数据库的基准名。
// 基座只接受这个名字或其下划线后缀变体（如 go_admin_pro_test_t6）：
// 用例间会清空数据，绝不能指向开发/生产库。允许后缀变体是为了让
// 不同工单的工作树可以并行跑集成测试，各自清各自的库互不干扰；
// CI 与 Makefile 默认仍使用基准名本身。
const TestDatabaseName = "go_admin_pro_test"

// IsTestDatabaseName 判断库名是否是允许的集成测试库：
// 基准名本身，或以「基准名_」开头的后缀变体。
func IsTestDatabaseName(name string) bool {
	return name == TestDatabaseName || strings.HasPrefix(name, TestDatabaseName+"_")
}

// DefaultTestDSN 是本机 docker compose MySQL（容器 go-admin-pro-mysql）的默认测试 DSN。
// Makefile 与 CI 会通过 GAP_DATABASE_DSN 显式传入；两者保持一致。
const DefaultTestDSN = "root:123456@tcp(127.0.0.1:3306)/" + TestDatabaseName + "?charset=utf8mb4&parseTime=True"

// ParseDSNDatabase 从 go-sql-driver 格式的 DSN 中提取数据库名。
// 定位规则与 config.ApplyDSNLoc 一致：最后一个 / 之后、? 之前的部分。
// 密码中含 / 的 DSN 不支持（与 ApplyDSNLoc 同样的已知限制）。
func ParseDSNDatabase(dsn string) (string, error) {
	slash := strings.LastIndex(dsn, "/")
	if slash < 0 {
		return "", fmt.Errorf("DSN 缺少数据库名段: %q", dsn)
	}
	name := dsn[slash+1:]
	if q := strings.IndexByte(name, '?'); q >= 0 {
		name = name[:q]
	}
	if name == "" {
		return "", fmt.Errorf("DSN 数据库名为空: %q", dsn)
	}
	return name, nil
}

// ServerDSN 返回去掉数据库名后的 DSN（形如 user:pass@tcp(host:port)/），
// 仅用于服务器级连接执行 CREATE DATABASE。参数段（charset 等）一并去掉，
// 建库语句不需要它们。
func ServerDSN(dsn string) (string, error) {
	if _, err := ParseDSNDatabase(dsn); err != nil {
		return "", err
	}
	slash := strings.LastIndex(dsn, "/")
	return dsn[:slash+1], nil
}
