package response_test

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"testing/iotest"
	"time"

	"github.com/lautung/go-admin-pro/internal/pkg/excel"
	"github.com/lautung/go-admin-pro/internal/pkg/response"
	"github.com/xuri/excelize/v2"
)

// guardRow 是导入 guard HTTP 映射测试用的行结构。
type guardRow struct {
	Name     string    `excel:"客户名称,required"`
	Age      int       `excel:"年龄"`
	JoinDate time.Time `excel:"加入日期"`
}

// guardHeader 与 guardRow 的表头一致。
var guardHeader = []any{"客户名称", "年龄", "加入日期"}

// buildGuardWorkbook 生成内存中的 xlsx，rows 中的每一行写入 A1 起始的位置。
func buildGuardWorkbook(t *testing.T, sheet string, rows [][]any) []byte {
	t.Helper()
	f := excelize.NewFile()
	defer f.Close()
	if sheet != "" && sheet != "Sheet1" {
		if err := f.SetSheetName("Sheet1", sheet); err != nil {
			t.Fatalf("rename sheet: %v", err)
		}
	} else {
		sheet = "Sheet1"
	}
	for i, row := range rows {
		cell, err := excelize.CoordinatesToCellName(1, i+1)
		if err != nil {
			t.Fatalf("cell name: %v", err)
		}
		if err := f.SetSheetRow(sheet, cell, &row); err != nil {
			t.Fatalf("set row: %v", err)
		}
	}
	buf, err := f.WriteToBuffer()
	if err != nil {
		t.Fatalf("write workbook: %v", err)
	}
	return buf.Bytes()
}

// guardValidationBizCode 与 guardBizCodes 是测试 handler 的演示业务码：
// guard 只提供类型化 Kind，最终业务码由接入方决定；
// 这里用固定映射演示前端可按码值区分「文件太大」「行数太多」「内容有误」。
const guardValidationBizCode = 2101

var guardBizCodes = map[excel.GuardErrorKind]int{
	excel.GuardErrFileTooLarge:    2201,
	excel.GuardErrTooManyRows:     2202,
	excel.GuardErrInvalidWorkbook: 2203,
	excel.GuardErrSheetNotFound:   2204,
	excel.GuardErrInvalidSchema:   2205,
	excel.GuardErrInvalidHeader:   2206,
	excel.GuardErrInternal:        500,
}

func guardBizCode(kind excel.GuardErrorKind) int { return guardBizCodes[kind] }

// guardBody 与本包 Body 的 {code,data,msg} 约定一致。
type guardBody struct {
	Code int             `json:"code"`
	Data json.RawMessage `json:"data"`
	Msg  string          `json:"msg"`
}

func guardWriteBody(w http.ResponseWriter, status, code int, data any, msg string) {
	raw, err := json.Marshal(data)
	if err != nil {
		raw = nil
	}
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(guardBody{Code: code, Data: raw, Msg: msg})
}

// guardRespond 把 guard 结果映射为 HTTP 语义：
// 成功与逐行校验失败是 200，限额与内容错误是 400，未预期错误是 500。
func guardRespond[T any](w http.ResponseWriter, items []T, err error) {
	if err == nil {
		guardWriteBody(w, http.StatusOK, 0, items, "")
		return
	}
	var ve *excel.ValidationError
	if errors.As(err, &ve) {
		guardWriteBody(w, http.StatusOK, guardValidationBizCode, ve, ve.Error())
		return
	}
	var ge *excel.GuardError
	if errors.As(err, &ge) {
		guardWriteBody(w, response.ImportGuardHTTPStatus(err), guardBizCode(ge.Kind), nil, ge.Msg)
		return
	}
	guardWriteBody(w, http.StatusInternalServerError, http.StatusInternalServerError, nil, "服务器内部错误")
}

// guardHandler 是测试用临时 HTTP handler：请求体即 Excel 文件。
func guardHandler[T any](g *excel.ImportGuard[T], opts excel.GuardOptions[T]) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		items, err := g.Import(r.Body, opts)
		guardRespond(w, items, err)
	}
}

// guardPost 发送导入请求并解析统一响应体，返回状态码、响应体与原始报文。
func guardPost(t *testing.T, url string, payload []byte) (int, guardBody, string) {
	t.Helper()
	resp, err := http.Post(url, "application/octet-stream", bytes.NewReader(payload))
	if err != nil {
		t.Fatalf("POST: %v", err)
	}
	defer resp.Body.Close()
	raw, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("read body: %v", err)
	}
	var gb guardBody
	if err := json.Unmarshal(raw, &gb); err != nil {
		t.Fatalf("decode body %q: %v", raw, err)
	}
	return resp.StatusCode, gb, string(raw)
}

func TestImportGuardHTTPStatusMapping(t *testing.T) {
	cases := []struct {
		name string
		err  error
		want int
	}{
		{"逐行校验失败", &excel.ValidationError{Total: 1}, http.StatusOK},
		{"文件过大", &excel.GuardError{Kind: excel.GuardErrFileTooLarge}, http.StatusBadRequest},
		{"行数超限", &excel.GuardError{Kind: excel.GuardErrTooManyRows}, http.StatusBadRequest},
		{"坏工作簿", &excel.GuardError{Kind: excel.GuardErrInvalidWorkbook}, http.StatusBadRequest},
		{"工作表不存在", &excel.GuardError{Kind: excel.GuardErrSheetNotFound}, http.StatusBadRequest},
		{"字段映射非法", &excel.GuardError{Kind: excel.GuardErrInvalidSchema}, http.StatusBadRequest},
		{"表头不合法", &excel.GuardError{Kind: excel.GuardErrInvalidHeader}, http.StatusBadRequest},
		{"未预期错误", &excel.GuardError{Kind: excel.GuardErrInternal}, http.StatusInternalServerError},
		{"非 guard 错误", errors.New("boom"), http.StatusInternalServerError},
		{"包装的非 guard 错误", fmt.Errorf("wrap: %w", errors.New("boom")), http.StatusInternalServerError},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := response.ImportGuardHTTPStatus(tc.err); got != tc.want {
				t.Errorf("ImportGuardHTTPStatus = %d, want %d", got, tc.want)
			}
		})
	}
}

func TestImportGuardHTTPFileTooLarge(t *testing.T) {
	g := excel.NewImportGuard[guardRow](1)
	srv := httptest.NewServer(guardHandler(g, excel.GuardOptions[guardRow]{}))
	t.Cleanup(srv.Close)

	payload := make([]byte, excel.DefaultMaxBytes+1)
	status, gb, _ := guardPost(t, srv.URL, payload)
	if status != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", status)
	}
	if gb.Code != guardBizCodes[excel.GuardErrFileTooLarge] {
		t.Errorf("code = %d, want 文件太大专属业务码 %d", gb.Code, guardBizCodes[excel.GuardErrFileTooLarge])
	}
	if gb.Msg != "文件超过大小上限" {
		t.Errorf("msg = %q", gb.Msg)
	}
}

func TestImportGuardHTTPBadRequest(t *testing.T) {
	g := excel.NewImportGuard[guardRow](1)
	srv := httptest.NewServer(guardHandler(g, excel.GuardOptions[guardRow]{}))
	t.Cleanup(srv.Close)

	t.Run("坏工作簿", func(t *testing.T) {
		status, gb, _ := guardPost(t, srv.URL, []byte("不是 xlsx 的内容"))
		if status != http.StatusBadRequest {
			t.Fatalf("status = %d, want 400", status)
		}
		if gb.Code != guardBizCodes[excel.GuardErrInvalidWorkbook] {
			t.Errorf("code = %d, want %d", gb.Code, guardBizCodes[excel.GuardErrInvalidWorkbook])
		}
		if gb.Msg != "无法解析的 Excel 文件，请使用导入模板填写" {
			t.Errorf("msg = %q", gb.Msg)
		}
	})

	// 「文件太大」与「内容有误」的业务码必须不同，前端才能区分。
	if guardBizCodes[excel.GuardErrFileTooLarge] == guardBizCodes[excel.GuardErrInvalidWorkbook] {
		t.Errorf("文件太大与内容有误的业务码相同，前端无法区分")
	}
}

func TestImportGuardHTTPValidation200(t *testing.T) {
	g := excel.NewImportGuard[guardRow](1)
	data := buildGuardWorkbook(t, "数据", [][]any{
		guardHeader,
		{"张三", 30, "2026-01-02"},
		{"李四", "abc", "2026-03-04"}, // 单元格解析错误
	})
	srv := httptest.NewServer(guardHandler(g, excel.GuardOptions[guardRow]{}))
	t.Cleanup(srv.Close)

	status, gb, _ := guardPost(t, srv.URL, data)
	if status != http.StatusOK {
		t.Fatalf("status = %d, want 200", status)
	}
	if gb.Code != guardValidationBizCode {
		t.Errorf("code = %d, want %d（业务码非 0，前端提示校验失败）", gb.Code, guardValidationBizCode)
	}
	if !strings.Contains(gb.Msg, "导入校验失败") {
		t.Errorf("msg = %q, want 包含「导入校验失败」", gb.Msg)
	}
	var result struct {
		Errors    []excel.CellError `json:"errors"`
		Total     int               `json:"total"`
		Truncated bool              `json:"truncated"`
	}
	if err := json.Unmarshal(gb.Data, &result); err != nil {
		t.Fatalf("decode data %q: %v", gb.Data, err)
	}
	if result.Total != 1 || result.Truncated {
		t.Errorf("total = %d truncated = %v, want 1/false", result.Total, result.Truncated)
	}
	if len(result.Errors) != 1 || result.Errors[0].Row != 3 || result.Errors[0].Column != "年龄" {
		t.Errorf("errors = %+v, want 第 3 行 年龄列", result.Errors)
	}
}

func TestImportGuardHTTPInternal500(t *testing.T) {
	marker := errors.New("内部数据库连接串 postgres://root:p@ssw0rd 与堆栈细节")
	g := excel.NewImportGuard[guardRow](1)

	// HTTP 级：响应体不包含任何内部细节，状态码 500。
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, err := g.Import(iotest.ErrReader(marker), excel.GuardOptions[guardRow]{})
		guardRespond[guardRow](w, nil, err)
	}))
	t.Cleanup(srv.Close)

	status, gb, raw := guardPost(t, srv.URL, []byte("任意内容"))
	if status != http.StatusInternalServerError {
		t.Fatalf("status = %d, want 500", status)
	}
	if gb.Code != http.StatusInternalServerError || gb.Msg != "服务器内部错误" {
		t.Errorf("code = %d msg = %q, want 500/服务器内部错误", gb.Code, gb.Msg)
	}
	for _, secret := range []string{"postgres", "p@ssw0rd", "堆栈", "数据库"} {
		if strings.Contains(raw, secret) {
			t.Errorf("响应泄露内部细节 %q：%s", secret, raw)
		}
	}
}
