//go:build integration

// 存储配额（issue #71，spec §4.2.3）：上传前统计 infra_file 未删除记录的
// size 之和，加上本次文件大小超过套餐 max_storage_mb 时返回 409「租户存储
// 空间不足」，且不写磁盘、不插 infra_file 行（配额校验失败不得写入业务数据）；
// max_storage_mb=0 与平台租户（未绑定套餐）均表示不限。
package infra_test

import (
	"bytes"
	"encoding/json"
	"io/fs"
	"mime/multipart"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/go-quick-code/go-admin-pro/internal/testutil"
)

// uploadForQuotaTest 以指定 token 上传一段 PNG 内容，返回 HTTP 状态与响应信封。
func uploadForQuotaTest(t *testing.T, env *testutil.IntegrationEnv, token, filename string, content []byte) (int, testutil.APIResponse) {
	t.Helper()

	var buf bytes.Buffer
	w := multipart.NewWriter(&buf)
	part, err := w.CreateFormFile("file", filename)
	if err != nil {
		t.Fatalf("构造 multipart 文件字段: %v", err)
	}
	if _, err := part.Write(content); err != nil {
		t.Fatalf("写入文件内容: %v", err)
	}
	if err := w.Close(); err != nil {
		t.Fatalf("关闭 multipart writer: %v", err)
	}
	req, err := http.NewRequest(http.MethodPost, env.APIPrefixURL("/infra/file/upload"), &buf)
	if err != nil {
		t.Fatalf("构造上传请求: %v", err)
	}
	req.Header.Set("Content-Type", w.FormDataContentType())
	req.Header.Set("Authorization", "Bearer "+token)
	resp, err := env.Server.Client().Do(req)
	if err != nil {
		t.Fatalf("上传请求: %v", err)
	}
	defer resp.Body.Close()
	var api testutil.APIResponse
	if err := json.NewDecoder(resp.Body).Decode(&api); err != nil {
		t.Fatalf("解析上传响应（HTTP %d）: %v", resp.StatusCode, err)
	}
	return resp.StatusCode, api
}

// countStorageDiskFiles 统计存储根目录下的磁盘文件数（含子目录），用于断言
// 被拒上传不残留磁盘文件。
func countStorageDiskFiles(t *testing.T, base string) int {
	t.Helper()
	count := 0
	err := filepath.WalkDir(base, func(_ string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if !d.IsDir() {
			count++
		}
		return nil
	})
	if err != nil && !os.IsNotExist(err) {
		t.Fatalf("遍历存储目录 %s: %v", base, err)
	}
	return count
}

// tenantFileStats 直查库返回租户未删除 infra_file 的行数与 size 之和。
func tenantFileStats(t *testing.T, env *testutil.IntegrationEnv, tenantID int64) (rows, totalSize int64) {
	t.Helper()
	var stat struct {
		FileRows  int64
		TotalSize int64
	}
	if err := env.DB.Raw(
		"SELECT COUNT(*) AS file_rows, COALESCE(SUM(size), 0) AS total_size FROM infra_file WHERE tenant_id = ? AND deleted_at IS NULL",
		tenantID,
	).Scan(&stat).Error; err != nil {
		t.Fatalf("统计租户 %d 文件: %v", tenantID, err)
	}
	return stat.FileRows, stat.TotalSize
}

// createStorageQuotaTenant 创建「存储套餐 + 绑定该套餐的租户」并登录租户管理员。
// 套餐菜单含 201 文件管理（按钮 2011 上传随父菜单展开），租户管理员可上传。
// 返回租户 ID 与租户管理员 token。
func createStorageQuotaTenant(t *testing.T, env *testutil.IntegrationEnv, name string, maxStorageMB int64) (int64, string) {
	t.Helper()

	admin := env.Login(t, "admin", "123456")
	status, api := env.Request(t, http.MethodPost, "/system/tenant-package", admin, map[string]any{
		"name": name + "-套餐", "status": 0, "menuIds": []int64{201}, "maxStorageMb": maxStorageMB,
	})
	mustOKForFileACL(t, "创建存储套餐", status, api)

	var pkg struct {
		ID int64 `json:"id"`
	}
	if err := env.DB.Raw("SELECT id FROM system_tenant_package WHERE name = ?", name+"-套餐").Scan(&pkg).Error; err != nil || pkg.ID == 0 {
		t.Fatalf("查询套餐 ID: %v（id=%d）", err, pkg.ID)
	}

	status, api = env.Request(t, http.MethodPost, "/system/tenant", admin, map[string]any{
		"name": name, "contactName": "存储配额", "status": 0, "packageId": pkg.ID,
	})
	mustOKForFileACL(t, "创建配额租户", status, api)

	var tenant struct {
		ID int64 `json:"id"`
	}
	if err := env.DB.Raw("SELECT id FROM system_tenant WHERE name = ?", name).Scan(&tenant).Error; err != nil || tenant.ID == 0 {
		t.Fatalf("查询租户 ID: %v（id=%d）", err, tenant.ID)
	}

	return tenant.ID, loginTenantAdminForInfraQuota(t, env, tenant.ID)
}

// loginTenantAdminForInfraQuota 登录指定租户内置管理员。
func loginTenantAdminForInfraQuota(t *testing.T, env *testutil.IntegrationEnv, tenantID int64) string {
	t.Helper()
	status, api := env.Request(t, http.MethodPost, "/system/auth/login", "", map[string]any{
		"username": "admin", "password": "123456", "tenantId": tenantID,
	})
	mustOKForFileACL(t, "登录租户管理员", status, api)
	var data struct {
		AccessToken string `json:"accessToken"`
	}
	if err := json.Unmarshal(api.Data, &data); err != nil || data.AccessToken == "" {
		t.Fatalf("解析登录响应: %v（data=%s）", err, string(api.Data))
	}
	return data.AccessToken
}

// TestIntegrationStorageQuotaBlocksUpload 套餐 maxStorageMb=1：
// 800KB 上传成功后，再传 800KB（800+800 > 1024KB）→ 409，且无新 infra_file
// 行、磁盘无残留；改传 200KB（800+200 ≤ 1024KB）→ 成功。
func TestIntegrationStorageQuotaBlocksUpload(t *testing.T) {
	env := testutil.NewIntegrationEnv(t)

	tenantID, tenantAdmin := createStorageQuotaTenant(t, env, "存储受限租户", 1)
	kb := 1024

	rows, size := tenantFileStats(t, env, tenantID)
	if rows != 0 || size != 0 {
		t.Fatalf("新租户应无文件，实际 rows=%d size=%d", rows, size)
	}

	// 第一次上传 800KB：800KB < 1MB，成功。
	status, api := uploadForQuotaTest(t, env, tenantAdmin, "first.png", make([]byte, 800*kb))
	if status != http.StatusOK || api.Code != 0 {
		t.Fatalf("首次上传：HTTP %d code %d msg %q，期望成功", status, api.Code, api.Msg)
	}
	rows, size = tenantFileStats(t, env, tenantID)
	if rows != 1 || size != int64(800*kb) {
		t.Fatalf("首次上传后应 1 行 800KB，实际 rows=%d size=%d", rows, size)
	}

	// 第二次上传 800KB：800+800 > 1024KB → 409。
	diskBefore := countStorageDiskFiles(t, env.Config.Storage.Local.BasePath)
	status, api = uploadForQuotaTest(t, env, tenantAdmin, "second.png", make([]byte, 800*kb))
	if status != http.StatusConflict {
		t.Fatalf("超限上传：HTTP %d code %d msg %q，期望 409", status, api.Code, api.Msg)
	}
	if !strings.Contains(api.Msg, "租户存储空间不足") {
		t.Fatalf("超限文案不符：%q", api.Msg)
	}
	rows, size = tenantFileStats(t, env, tenantID)
	if rows != 1 || size != int64(800*kb) {
		t.Fatalf("被拒上传不得写 infra_file，实际 rows=%d size=%d", rows, size)
	}
	if diskAfter := countStorageDiskFiles(t, env.Config.Storage.Local.BasePath); diskAfter != diskBefore {
		t.Fatalf("被拒上传不得写磁盘：失败前 %d 个文件，失败后 %d 个", diskBefore, diskAfter)
	}

	// 200KB（800+200 ≤ 1024KB）→ 成功，证明拒绝后配额状态未被污染。
	status, api = uploadForQuotaTest(t, env, tenantAdmin, "third.png", make([]byte, 200*kb))
	if status != http.StatusOK || api.Code != 0 {
		t.Fatalf("边界内上传：HTTP %d code %d msg %q，期望成功", status, api.Code, api.Msg)
	}
	rows, size = tenantFileStats(t, env, tenantID)
	if rows != 2 || size != int64(1000*kb) {
		t.Fatalf("边界内上传后应 2 行 1000KB，实际 rows=%d size=%d", rows, size)
	}
}

// TestIntegrationStorageQuotaUnlimited maxStorageMb=0 与平台租户均不限：
// 套餐绑定租户传 1.5MB、平台租户传 1.5MB 都成功。
func TestIntegrationStorageQuotaUnlimited(t *testing.T) {
	env := testutil.NewIntegrationEnv(t)

	// 套餐 maxStorageMb=0 → 该项不限。
	zeroTenantID, zeroAdmin := createStorageQuotaTenant(t, env, "存储不限租户", 0)
	status, api := uploadForQuotaTest(t, env, zeroAdmin, "big.png", make([]byte, 1536*1024))
	if status != http.StatusOK || api.Code != 0 {
		t.Fatalf("maxStorageMb=0 租户大文件上传：HTTP %d code %d msg %q，期望成功", status, api.Code, api.Msg)
	}
	if rows, _ := tenantFileStats(t, env, zeroTenantID); rows != 1 {
		t.Fatalf("不限租户应有 1 行文件，实际 %d", rows)
	}

	// 平台租户（ID=1，未绑定套餐）永远不限。
	platformAdmin := env.Login(t, "admin", "123456")
	status, api = uploadForQuotaTest(t, env, platformAdmin, "platform.png", make([]byte, 1536*1024))
	if status != http.StatusOK || api.Code != 0 {
		t.Fatalf("平台租户大文件上传：HTTP %d code %d msg %q，期望成功", status, api.Code, api.Msg)
	}
	if rows, size := tenantFileStats(t, env, 1); rows != 1 || size != int64(1536*1024) {
		t.Fatalf("平台租户应有 1 行 1.5MB 文件，实际 rows=%d size=%d", rows, size)
	}
}
