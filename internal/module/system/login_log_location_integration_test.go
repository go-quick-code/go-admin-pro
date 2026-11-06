//go:build integration

// 登录日志归属地集成测试（issue #82，spec §4.8.4/§4.8.5）：
//   - 未配置 ip-location.xdb-path：分页正常返回、loginLocation 为空；
//   - 配置自制极小 xdb（经 GAP_IP_LOCATION_XDB_PATH 注入）：
//     loginLocation 按 xdb 内容展示，定位不影响分页行为。
package system_test

import (
	"encoding/binary"
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/go-quick-code/go-admin-pro/internal/module/system/model"
	"github.com/go-quick-code/go-admin-pro/internal/pkg/tenant"
	"github.com/go-quick-code/go-admin-pro/internal/testutil"
)

// buildLoginLogXdb 构造极小 ip2region xdb（v2 结构，与 iploc 驱动测试同款
// 布局）：单网段 0.0.0.0/0 → 固定区域串。system_test 无法复用 iploc 包内
// 测试工具（未导出），此处独立落盘，仓库依旧不引入任何三方数据文件。
func buildLoginLogXdb(t *testing.T, region string) string {
	t.Helper()
	const headerLen = 256
	vectorLen := 256 * 256 * 8
	segPtr := uint32(headerLen + vectorLen)
	dataPtr := segPtr + 14
	buf := make([]byte, int(dataPtr)+len(region))

	binary.LittleEndian.PutUint16(buf[0:], 2) // 结构版本
	binary.LittleEndian.PutUint16(buf[2:], 1) // 向量索引策略
	binary.LittleEndian.PutUint32(buf[8:], segPtr)
	binary.LittleEndian.PutUint32(buf[12:], segPtr)
	binary.LittleEndian.PutUint16(buf[16:], 4) // IPv4
	binary.LittleEndian.PutUint16(buf[18:], 4)

	for i := 0; i < 256*256; i++ {
		off := headerLen + i*8
		binary.LittleEndian.PutUint32(buf[off:], segPtr)
		binary.LittleEndian.PutUint32(buf[off+4:], segPtr)
	}
	copy(buf[segPtr:segPtr+4], []byte{0, 0, 0, 0})
	copy(buf[segPtr+4:segPtr+8], []byte{0xFF, 0xFF, 0xFF, 0xFF})
	binary.LittleEndian.PutUint16(buf[segPtr+8:], uint16(len(region)))
	binary.LittleEndian.PutUint32(buf[segPtr+10:], dataPtr)
	copy(buf[dataPtr:], region)

	path := filepath.Join(t.TempDir(), "login-log.xdb")
	if err := os.WriteFile(path, buf, 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

// createLoginLog 直接经 DB 写一条登录日志（走 HTTP 登录失败链路会引入
// 验证码/锁定等无关耦合，此处只验证「分页 + 归属地补全」的展示契约）。
func createLoginLog(t *testing.T, env *testutil.IntegrationEnv, username, ip string) {
	t.Helper()
	row := &model.LoginLog{
		TenantID:  1,
		UserID:    1,
		Username:  username,
		LoginIP:   ip,
		LoginType: 1,
		Result:    0,
		Message:   "登录成功",
		LoginTime: time.Now(),
	}
	if err := tenant.Skip(env.DB).Create(row).Error; err != nil {
		t.Fatalf("写入登录日志: %v", err)
	}
}

func loginLogPage(t *testing.T, env *testutil.IntegrationEnv, token string) []map[string]any {
	t.Helper()
	status, api := env.Request(t, http.MethodGet, "/system/login-log/page?pageNo=1&pageSize=10", token, nil)
	if status != http.StatusOK || api.Code != 0 {
		t.Fatalf("登录日志分页应成功：HTTP %d code %d msg %q", status, api.Code, api.Msg)
	}
	var page struct {
		List []map[string]any `json:"list"`
	}
	if err := json.Unmarshal(api.Data, &page); err != nil {
		t.Fatalf("解析分页: %v（data=%s）", err, string(api.Data))
	}
	return page.List
}

// TestIntegrationLoginLogLocationWithoutXdb 未配置 xdb：分页正常、归属地为空。
func TestIntegrationLoginLogLocationWithoutXdb(t *testing.T) {
	t.Setenv("GAP_IP_LOCATION_XDB_PATH", "")
	env := testutil.NewIntegrationEnv(t)
	token := env.Login(t, "admin", "123456")
	createLoginLog(t, env, "admin", "88.88.88.88")

	list := loginLogPage(t, env, token)
	if len(list) == 0 {
		t.Fatal("应能查到写入的登录日志")
	}
	for _, row := range list {
		if loc, _ := row["loginLocation"].(string); loc != "" {
			t.Fatalf("未配置 xdb 时归属地应为空，实际 %q", loc)
		}
	}
}

// TestIntegrationLoginLogLocationWithXdb 配置 xdb：分页补归属地。
func TestIntegrationLoginLogLocationWithXdb(t *testing.T) {
	t.Setenv("GAP_IP_LOCATION_XDB_PATH", buildLoginLogXdb(t, "中国|0|广东省|深圳市|电信"))
	env := testutil.NewIntegrationEnv(t)
	token := env.Login(t, "admin", "123456")
	// 已知 IP（在自制 xdb 全网段覆盖内）与无 IP 行（不应有归属地）。
	createLoginLog(t, env, "admin", "88.88.88.88")
	createLoginLog(t, env, "noip", "")

	list := loginLogPage(t, env, token)
	found := 0
	for _, row := range list {
		username, _ := row["username"].(string)
		loc, _ := row["loginLocation"].(string)
		if username == "noip" && loc != "" {
			t.Fatalf("无 IP 行不应有归属地，实际 %q", loc)
		}
		if username == "admin" && row["loginIp"] == "88.88.88.88" {
			found++
			if loc != "中国 广东省 深圳市 电信" {
				t.Fatalf("归属地应为格式化区域串，实际 %q", loc)
			}
		}
	}
	if found != 1 {
		t.Fatalf("应恰好命中一条 88.88.88.88 日志，实际 %d（list=%d）", found, len(list))
	}
}
