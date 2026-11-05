package service_test

import (
	"bytes"
	"context"
	"errors"
	"mime/multipart"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/lautung/go-admin-pro/internal/config"
	"github.com/lautung/go-admin-pro/internal/module/infra/model"
	"github.com/lautung/go-admin-pro/internal/module/infra/repository"
	"github.com/lautung/go-admin-pro/internal/module/infra/service"
	"github.com/lautung/go-admin-pro/internal/pkg/database"
	"github.com/lautung/go-admin-pro/internal/pkg/tenant"
	"github.com/lautung/go-admin-pro/internal/pkg/xerr"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

// 文件按业务归属（biz_type/biz_id）的通用能力测试（issue #17）：
// 合同附件复用 infra_file，通用侧只认「业务类型 + 业务 ID」，
// 归属校验必须保证一个业务只能看到/删除自己名下的文件（跨业务与跨租户都 404）。

func newFileTestService(t *testing.T) (*service.Service, *config.Config) {
	t.Helper()

	db, err := gorm.Open(sqlite.Open("file:"+t.Name()+"?mode=memory&cache=shared"), &gorm.Config{
		Logger: logger.Default.LogMode(logger.Silent),
	})
	if err != nil {
		t.Fatalf("打开 sqlite: %v", err)
	}
	sqlDB, err := db.DB()
	if err != nil {
		t.Fatalf("取 sql.DB: %v", err)
	}
	sqlDB.SetMaxOpenConns(1)
	t.Cleanup(func() { _ = sqlDB.Close() })

	if err := db.AutoMigrate(&model.File{}); err != nil {
		t.Fatalf("建表: %v", err)
	}
	if err := db.Use(database.NewTenantPlugin(database.TenantTables)); err != nil {
		t.Fatalf("安装租户插件: %v", err)
	}

	cfg := &config.Config{}
	cfg.App.APIPrefix = "/admin-api"
	cfg.Storage.Local.BasePath = t.TempDir()
	return service.New(repository.New(db), cfg, nil), cfg
}

// multipartHeader 构造一个真实解析出来的 multipart.FileHeader（走标准库分片解析，
// 不上传、不落盘），供服务层校验与入库路径使用。
func multipartHeader(t *testing.T, filename string, content []byte) *multipart.FileHeader {
	t.Helper()

	var buf bytes.Buffer
	w := multipart.NewWriter(&buf)
	part, err := w.CreateFormFile("file", filename)
	if err != nil {
		t.Fatalf("构造 multipart 字段: %v", err)
	}
	if _, err := part.Write(content); err != nil {
		t.Fatalf("写入 multipart 内容: %v", err)
	}
	if err := w.Close(); err != nil {
		t.Fatalf("关闭 multipart writer: %v", err)
	}

	r := multipart.NewReader(bytes.NewReader(buf.Bytes()), w.Boundary())
	form, err := r.ReadForm(1 << 20)
	if err != nil {
		t.Fatalf("解析 multipart: %v", err)
	}
	t.Cleanup(func() { _ = form.RemoveAll() })
	files := form.File["file"]
	if len(files) != 1 {
		t.Fatalf("解析 multipart 应得到 1 个文件，实际 %d", len(files))
	}
	return files[0]
}

func requireStatus(t *testing.T, err error, want int, msgContains string) {
	t.Helper()
	if err == nil {
		t.Fatalf("期望 HTTP %d 错误，实际 nil", want)
	}
	var e *xerr.Error
	if !errors.As(err, &e) || e.Status != want {
		t.Fatalf("期望 HTTP %d 错误，实际 %v", want, err)
	}
	if msgContains != "" && !strings.Contains(e.Msg, msgContains) {
		t.Fatalf("错误文案应包含 %q，实际 %q", msgContains, e.Msg)
	}
}

// TestFileBizScoping 业务归属隔离：上传时写入 biz_type/biz_id，列表只返回该业务
// 的文件；跨业务删除 404 且不误删；正确归属可删除记录与磁盘文件。
func TestFileBizScoping(t *testing.T) {
	svc, cfg := newFileTestService(t)
	ctx := tenant.WithTenantID(context.Background(), 1)

	uploaded, err := svc.UploadFileForBiz(ctx, multipartHeader(t, "合同扫描件.pdf", []byte("hello")), "crm_contract", 7)
	if err != nil {
		t.Fatalf("按业务上传文件: %v", err)
	}
	if uploaded.BizType != "crm_contract" || uploaded.BizID != 7 {
		t.Fatalf("上传应写入业务归属 crm_contract/7，实际 %q/%d", uploaded.BizType, uploaded.BizID)
	}
	if uploaded.Size != int64(len("hello")) {
		t.Fatalf("落库大小应为 5，实际 %d", uploaded.Size)
	}
	absPath := filepath.Join(cfg.Storage.Local.BasePath, filepath.FromSlash(uploaded.Path))
	if _, err := os.Stat(absPath); err != nil {
		t.Fatalf("上传后磁盘文件应存在 %s: %v", absPath, err)
	}

	files, err := svc.ListFilesByBiz(ctx, "crm_contract", 7)
	if err != nil || len(files) != 1 || files[0].ID != uploaded.ID {
		t.Fatalf("按业务列表应返回刚上传的文件，实际 %d 条 err=%v", len(files), err)
	}
	if files, err := svc.ListFilesByBiz(ctx, "crm_contract", 8); err != nil || len(files) != 0 {
		t.Fatalf("其他业务不应看到该文件，实际 %d 条 err=%v", len(files), err)
	}
	if files, err := svc.ListFilesByBiz(ctx, "crm_opportunity", 7); err != nil || len(files) != 0 {
		t.Fatalf("其他业务类型不应看到该文件，实际 %d 条 err=%v", len(files), err)
	}

	// 跨业务删除：404 且文件仍在。
	requireStatus(t, svc.DeleteFileByBiz(ctx, "crm_contract", 8, uploaded.ID), 404, "不存在")
	if files, _ := svc.ListFilesByBiz(ctx, "crm_contract", 7); len(files) != 1 {
		t.Fatalf("跨业务删除不应误删文件，实际剩 %d 条", len(files))
	}

	// 跨租户删除：404 且文件仍在（租户插件在仓储层过滤）。
	ctx2 := tenant.WithTenantID(context.Background(), 2)
	requireStatus(t, svc.DeleteFileByBiz(ctx2, "crm_contract", 7, uploaded.ID), 404, "不存在")
	if files, _ := svc.ListFilesByBiz(ctx, "crm_contract", 7); len(files) != 1 {
		t.Fatalf("跨租户删除不应误删文件，实际剩 %d 条", len(files))
	}

	// 正确归属：删除记录与磁盘文件。
	if err := svc.DeleteFileByBiz(ctx, "crm_contract", 7, uploaded.ID); err != nil {
		t.Fatalf("删除归属文件: %v", err)
	}
	if files, _ := svc.ListFilesByBiz(ctx, "crm_contract", 7); len(files) != 0 {
		t.Fatalf("删除后列表应为空，实际 %d 条", len(files))
	}
	if _, err := os.Stat(absPath); !os.IsNotExist(err) {
		t.Fatalf("删除后磁盘文件应不存在，实际 err=%v", err)
	}
}

// TestUploadFileForBizValidatesBizKey 通用业务归属参数非法时 400，不落库。
func TestUploadFileForBizValidatesBizKey(t *testing.T) {
	svc, _ := newFileTestService(t)
	ctx := tenant.WithTenantID(context.Background(), 1)
	fh := multipartHeader(t, "a.pdf", []byte("x"))

	for _, tc := range []struct {
		name    string
		bizType string
		bizID   int64
	}{
		{name: "业务类型为空", bizType: "", bizID: 1},
		{name: "业务 ID 为 0", bizType: "crm_contract", bizID: 0},
		{name: "业务 ID 为负", bizType: "crm_contract", bizID: -1},
		{name: "业务类型超长", bizType: strings.Repeat("a", 33), bizID: 1},
	} {
		requireStatus(t, func() error {
			_, err := svc.UploadFileForBiz(ctx, fh, tc.bizType, tc.bizID)
			return err
		}(), 400, "")
	}
}

// TestUploadFileForBizReusesInfraLimits 业务上传复用 infra 的 20MB 与扩展名白名单口径。
func TestUploadFileForBizReusesInfraLimits(t *testing.T) {
	svc, _ := newFileTestService(t)
	ctx := tenant.WithTenantID(context.Background(), 1)

	oversize := &multipart.FileHeader{Filename: "big.pdf", Size: 20<<20 + 1}
	requireStatus(t, func() error {
		_, err := svc.UploadFileForBiz(ctx, oversize, "crm_contract", 1)
		return err
	}(), 400, "20MB")

	requireStatus(t, func() error {
		_, err := svc.UploadFileForBiz(ctx, multipartHeader(t, "evil.exe", []byte("x")), "crm_contract", 1)
		return err
	}(), 400, "不支持")
}
