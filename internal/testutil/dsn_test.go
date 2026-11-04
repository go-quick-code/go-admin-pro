package testutil_test

import (
	"testing"

	"github.com/lautung/go-admin-pro/internal/testutil"
)

func TestParseDSNDatabase(t *testing.T) {
	cases := []struct {
		name string
		dsn  string
		want string
	}{
		{"带参数", "root:123456@tcp(127.0.0.1:3306)/go_admin_pro_test?charset=utf8mb4&parseTime=True", "go_admin_pro_test"},
		{"无参数", "root:123456@tcp(127.0.0.1:3306)/go_admin_pro", "go_admin_pro"},
		{"loc 参数含斜杠编码", "root:123456@tcp(127.0.0.1:3306)/go_admin_pro_test?loc=Asia%2FShanghai", "go_admin_pro_test"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := testutil.ParseDSNDatabase(tc.dsn)
			if err != nil {
				t.Fatalf("ParseDSNDatabase(%q): %v", tc.dsn, err)
			}
			if got != tc.want {
				t.Fatalf("ParseDSNDatabase(%q) = %q, want %q", tc.dsn, got, tc.want)
			}
		})
	}

	for _, dsn := range []string{
		"root:123456@tcp(127.0.0.1:3306)",            // 无斜杠
		"root:123456@tcp(127.0.0.1:3306)/",           // 空库名
		"root:123456@tcp(127.0.0.1:3306)/?loc=Local", // 参数前空库名
	} {
		if _, err := testutil.ParseDSNDatabase(dsn); err == nil {
			t.Fatalf("ParseDSNDatabase(%q) 期望报错，实际成功", dsn)
		}
	}
}

func TestServerDSN(t *testing.T) {
	got, err := testutil.ServerDSN("root:123456@tcp(127.0.0.1:3306)/go_admin_pro_test?charset=utf8mb4&parseTime=True")
	if err != nil {
		t.Fatalf("ServerDSN: %v", err)
	}
	want := "root:123456@tcp(127.0.0.1:3306)/"
	if got != want {
		t.Fatalf("ServerDSN = %q, want %q", got, want)
	}

	if _, err := testutil.ServerDSN("root:123456@tcp(127.0.0.1:3306)"); err == nil {
		t.Fatal("ServerDSN 对缺数据库名的 DSN 期望报错，实际成功")
	}
}

// TestDefaultTestDSN 守住默认 DSN 与独立测试库的一致性：
// 默认值漂移到其他库会让无 env 的本地运行直接打错库。
func TestDefaultTestDSN(t *testing.T) {
	name, err := testutil.ParseDSNDatabase(testutil.DefaultTestDSN)
	if err != nil {
		t.Fatalf("ParseDSNDatabase(DefaultTestDSN): %v", err)
	}
	if name != testutil.TestDatabaseName {
		t.Fatalf("DefaultTestDSN 指向 %q, want %q", name, testutil.TestDatabaseName)
	}
}
