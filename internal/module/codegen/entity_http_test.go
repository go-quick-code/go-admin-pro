package codegen_test

import (
	"archive/zip"
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"

	"github.com/go-quick-code/go-admin-pro/internal/module/codegen"
)

// TestWriteFiles 落盘助手：默认拒绝覆盖，-overwrite 解锁；路径按 / 分隔。
func TestWriteFiles(t *testing.T) {
	dir := t.TempDir()
	files := map[string]string{
		"a/b/c.txt": "内容",
		"top.md":    "# 顶",
	}
	written, err := codegen.WriteFiles(dir, files, false)
	if err != nil {
		t.Fatalf("WriteFiles: %v", err)
	}
	if len(written) != 2 {
		t.Fatalf("写入 %d 个文件，期望 2", len(written))
	}
	raw, err := os.ReadFile(filepath.Join(dir, "a", "b", "c.txt"))
	if err != nil || string(raw) != "内容" {
		t.Fatalf("嵌套路径写入不符: %v %q", err, string(raw))
	}

	// 再写同路径：默认拒绝。
	if _, err := codegen.WriteFiles(dir, map[string]string{"top.md": "改动"}, false); err == nil {
		t.Fatal("默认应拒绝覆盖已存在文件")
	}
	if _, err := codegen.WriteFiles(dir, map[string]string{"top.md": "改动"}, true); err != nil {
		t.Fatalf("overwrite=true 应允许覆盖: %v", err)
	}
	raw, _ = os.ReadFile(filepath.Join(dir, "top.md"))
	if string(raw) != "改动" {
		t.Fatalf("覆盖后内容不符: %q", string(raw))
	}
}

// TestGenerateEntityHandlerEntityEndpoint HTTP 端点冒烟：
// 合法元信息 → 200 ZIP（条目与文件集一致）；非法元信息 → 400 中文文案。
func TestGenerateEntityHandlerEntityEndpoint(t *testing.T) {
	gin.SetMode(gin.TestMode)
	engine := gin.New()
	rg := engine.Group("/")
	handler := codegen.NewHandler(nil, func(string) gin.HandlerFunc {
		return func(c *gin.Context) { c.Next() }
	})
	handler.RegisterProtected(rg)
	srv := httptest.NewServer(engine)
	t.Cleanup(srv.Close)

	t.Run("合法元信息返回 zip", func(t *testing.T) {
		spec := loadProductSpec(t)
		body, err := json.Marshal(spec)
		if err != nil {
			t.Fatal(err)
		}
		resp, err := http.Post(srv.URL+"/infra/codegen/generate-entity", "application/json", bytes.NewReader(body))
		if err != nil {
			t.Fatal(err)
		}
		defer resp.Body.Close()
		if resp.StatusCode != http.StatusOK {
			t.Fatalf("HTTP %d，期望 200", resp.StatusCode)
		}
		if ct := resp.Header.Get("Content-Type"); ct != "application/zip" {
			t.Fatalf("Content-Type %q，期望 application/zip", ct)
		}
		zr, err := zip.NewReader(bytes.NewReader(mustReadAll(t, resp)), int64(resp.ContentLength))
		if err != nil {
			t.Fatalf("响应不是合法 zip: %v", err)
		}
		if len(zr.File) == 0 {
			t.Fatal("zip 不应为空")
		}
		found := false
		for _, f := range zr.File {
			if strings.HasSuffix(f.Name, "service/product.go") {
				found = true
			}
		}
		if !found {
			t.Error("zip 应包含 service/product.go")
		}
	})

	t.Run("非法元信息 400", func(t *testing.T) {
		body := `{"module":"CRM","entity":"product","tableName":"crm_product","comment":"产品","fields":[]}`
		resp, err := http.Post(srv.URL+"/infra/codegen/generate-entity", "application/json", strings.NewReader(body))
		if err != nil {
			t.Fatal(err)
		}
		defer resp.Body.Close()
		if resp.StatusCode != http.StatusBadRequest {
			t.Fatalf("HTTP %d，期望 400", resp.StatusCode)
		}
		var api struct {
			Code int    `json:"code"`
			Msg  string `json:"msg"`
		}
		if err := json.NewDecoder(resp.Body).Decode(&api); err != nil {
			t.Fatal(err)
		}
		if !strings.Contains(api.Msg, "模块名") {
			t.Fatalf("错误文案应说明模块名，实际 %q", api.Msg)
		}
	})
}

func mustReadAll(t *testing.T, resp *http.Response) []byte {
	t.Helper()
	var buf bytes.Buffer
	if _, err := buf.ReadFrom(resp.Body); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}
