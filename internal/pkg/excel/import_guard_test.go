package excel_test

import (
	"archive/zip"
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"testing/iotest"
	"time"

	"github.com/lautung/go-admin-pro/internal/pkg/excel"
	"github.com/xuri/excelize/v2"
)

// guardRow 是导入 guard 测试用的行结构，与 guard 之外的测试互不依赖。
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

// guardMaxUnzipTestBytes 与 guard 的解压上限一致（80MB）。
const guardMaxUnzipTestBytes int64 = 80 << 20

// buildGuardZerosZip 生成一个声明解压后大小为 uncompressed 的 zip，
// 全部是零字节，deflate 后只有几十 KB，用小文件锁定解压上限。
func buildGuardZerosZip(t *testing.T, uncompressed int64) []byte {
	t.Helper()
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	w, err := zw.Create("zeros.bin")
	if err != nil {
		t.Fatalf("zip create: %v", err)
	}
	if _, err := io.CopyN(w, zerosReader{}, uncompressed); err != nil {
		t.Fatalf("zip write: %v", err)
	}
	if err := zw.Close(); err != nil {
		t.Fatalf("zip close: %v", err)
	}
	return buf.Bytes()
}

type zerosReader struct{}

func (zerosReader) Read(p []byte) (int, error) {
	for i := range p {
		p[i] = 0
	}
	return len(p), nil
}

func TestImportGuardImport(t *testing.T) {
	data := buildGuardWorkbook(t, "数据", [][]any{
		guardHeader,
		{"张三", 30, "2026-01-02"},
		{"李四", 28, "2026-03-04"},
	})
	g := excel.NewImportGuard[guardRow](0) // <=0 用默认并发
	seen := []int{}
	items, err := g.Import(bytes.NewReader(data), excel.GuardOptions[guardRow]{
		Validate: func(row int, v *guardRow) error {
			seen = append(seen, row)
			return nil
		},
	})
	if err != nil {
		t.Fatalf("Import: %v", err)
	}
	if len(items) != 2 {
		t.Fatalf("got %d items, want 2: %+v", len(items), items)
	}
	if items[0].Name != "张三" || items[0].Age != 30 {
		t.Errorf("unexpected first item: %+v", items[0])
	}
	if items[1].Name != "李四" || items[1].Age != 28 {
		t.Errorf("unexpected second item: %+v", items[1])
	}
	// Validate 透传 helper 语义：收到 Excel 行号，数据行从第 2 行开始。
	if len(seen) != 2 || seen[0] != 2 || seen[1] != 3 {
		t.Errorf("validate rows = %v, want [2 3]", seen)
	}
}

func TestImportGuardLocationPassthrough(t *testing.T) {
	data := buildGuardWorkbook(t, "数据", [][]any{
		guardHeader,
		{"张三", 30, "2026-01-02 10:00:00"},
	})
	// 选用与常见机器时区都不同的偏移，若 guard 丢弃 Location 则必然失败。
	zone := time.FixedZone("guard测试时区", 5*3600+30*60)
	g := excel.NewImportGuard[guardRow](1)
	items, err := g.Import(bytes.NewReader(data), excel.GuardOptions[guardRow]{Location: zone})
	if err != nil {
		t.Fatalf("Import: %v", err)
	}
	want := time.Date(2026, 1, 2, 10, 0, 0, 0, zone)
	if !items[0].JoinDate.Equal(want) {
		t.Errorf("join date = %v, want %v", items[0].JoinDate, want)
	}
}

func TestImportGuardQueueing(t *testing.T) {
	t.Run("default concurrency 2", func(t *testing.T) {
		assertGuardQueueing(t, 0, 2)
	})
	t.Run("explicit concurrency 1", func(t *testing.T) {
		assertGuardQueueing(t, 1, 1)
	})
}

// assertGuardQueueing 同时发起 3 个导入，锁定：全部成功（排队而非失败），
// 且同时在解析的导入数不超过上限。Validate 在持锁区间内计数。
func assertGuardQueueing(t *testing.T, configured int, wantMax int64) {
	t.Helper()
	g := excel.NewImportGuard[guardRow](configured)
	data := buildGuardWorkbook(t, "数据", [][]any{
		guardHeader,
		{"张三", 30, "2026-01-02"},
	})
	var inFlight, maxInFlight atomic.Int64
	validate := func(row int, v *guardRow) error {
		cur := inFlight.Add(1)
		for {
			m := maxInFlight.Load()
			if cur <= m || maxInFlight.CompareAndSwap(m, cur) {
				break
			}
		}
		time.Sleep(200 * time.Millisecond) // 占住槽位，制造并发重叠
		inFlight.Add(-1)
		return nil
	}

	const total = 3
	type outcome struct {
		items []guardRow
		err   error
	}
	results := make(chan outcome, total)
	ready := make(chan struct{}, total)
	start := make(chan struct{})
	for i := 0; i < total; i++ {
		go func() {
			ready <- struct{}{}
			<-start
			items, err := g.Import(bytes.NewReader(data), excel.GuardOptions[guardRow]{Validate: validate})
			results <- outcome{items, err}
		}()
	}
	for i := 0; i < total; i++ {
		<-ready
	}
	close(start)

	for i := 0; i < total; i++ {
		select {
		case r := <-results:
			if r.err != nil {
				t.Errorf("第 %d 个并发导入失败（应排队等待而非失败）：%v", i+1, r.err)
				continue
			}
			if len(r.items) != 1 {
				t.Errorf("第 %d 个并发导入得到 %d 行，want 1", i+1, len(r.items))
			}
		case <-time.After(10 * time.Second):
			t.Fatal("等待排队导入完成超时：信号量可能没有释放")
		}
	}
	if got := maxInFlight.Load(); got != wantMax {
		t.Errorf("最大并发 = %d, want %d", got, wantMax)
	}
}

func TestImportGuardUnzipLimit(t *testing.T) {
	g := excel.NewImportGuard[guardRow](1)

	t.Run("解压后超过 80MB 拒绝", func(t *testing.T) {
		// 80MB+1 的声明大小，deflate 后只有几十 KB。
		data := buildGuardZerosZip(t, guardMaxUnzipTestBytes+1)
		_, err := g.Import(bytes.NewReader(data), excel.GuardOptions[guardRow]{})
		if !errors.Is(err, excel.ErrFileTooLarge) {
			t.Fatalf("error = %v, want ErrFileTooLarge", err)
		}
	})

	t.Run("解压后恰好 80MB 进入解析", func(t *testing.T) {
		// 恰好 80MB 不应按文件过大拒绝；内容不是 xlsx，应归类为坏工作簿。
		// 若 guard 传给 helper 的解压上限小于 80MB，这里会错误地返回 ErrFileTooLarge。
		data := buildGuardZerosZip(t, guardMaxUnzipTestBytes)
		_, err := g.Import(bytes.NewReader(data), excel.GuardOptions[guardRow]{})
		if errors.Is(err, excel.ErrFileTooLarge) {
			t.Fatalf("恰好 80MB 不应按文件过大拒绝，got %v", err)
		}
		if !errors.Is(err, excel.ErrInvalidWorkbook) {
			t.Fatalf("error = %v, want ErrInvalidWorkbook", err)
		}
	})
}

// guardBadSchemaRow 触发 helper 的字段映射错误：chan 字段不支持导入。
type guardBadSchemaRow struct {
	Ch chan int `excel:"通道"`
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

// guardBody 与 internal/pkg/response 的 {code,data,msg} 约定一致。
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
		guardWriteBody(w, excel.HTTPStatus(err), guardBizCode(ge.Kind), nil, ge.Msg)
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

func assertGuardError(t *testing.T, err error, kind excel.GuardErrorKind, msg string) {
	t.Helper()
	var ge *excel.GuardError
	if !errors.As(err, &ge) {
		t.Fatalf("error = %v (%T), want *excel.GuardError", err, err)
	}
	if ge.Kind != kind {
		t.Errorf("kind = %d, want %d", ge.Kind, kind)
	}
	if ge.Msg != msg {
		t.Errorf("msg = %q, want %q", ge.Msg, msg)
	}
	if got := excel.HTTPStatus(err); got != http.StatusBadRequest {
		t.Errorf("HTTPStatus = %d, want 400", got)
	}
}

func TestImportGuardErrorKinds(t *testing.T) {
	g := excel.NewImportGuard[guardRow](1)
	opts := excel.GuardOptions[guardRow]{}

	t.Run("文件过大", func(t *testing.T) {
		_, err := g.Import(bytes.NewReader(make([]byte, excel.DefaultMaxBytes+1)), opts)
		assertGuardError(t, err, excel.GuardErrFileTooLarge, "文件超过大小上限")
	})

	t.Run("坏工作簿", func(t *testing.T) {
		_, err := g.Import(strings.NewReader("不是 xlsx 的内容"), opts)
		assertGuardError(t, err, excel.GuardErrInvalidWorkbook, "无法解析的 Excel 文件，请使用导入模板填写")
	})

	t.Run("工作表不存在", func(t *testing.T) {
		data := buildGuardWorkbook(t, "数据", [][]any{guardHeader, {"张三", 30, "2026-01-02"}})
		_, err := g.Import(bytes.NewReader(data), excel.GuardOptions[guardRow]{Sheet: "不存在"})
		assertGuardError(t, err, excel.GuardErrSheetNotFound, "工作表不存在")
	})

	t.Run("字段映射非法", func(t *testing.T) {
		gb := excel.NewImportGuard[guardBadSchemaRow](1)
		_, err := gb.Import(strings.NewReader(""), excel.GuardOptions[guardBadSchemaRow]{})
		assertGuardError(t, err, excel.GuardErrInvalidSchema, "字段映射不合法")
	})

	t.Run("未知列", func(t *testing.T) {
		header := append(append([]any{}, guardHeader...), "额外列")
		data := buildGuardWorkbook(t, "数据", [][]any{header, {"张三", 30, "2026-01-02", "x"}})
		_, err := g.Import(bytes.NewReader(data), opts)
		assertGuardError(t, err, excel.GuardErrInvalidHeader, "表头与导入模板不匹配")
		// 底层保留 helper 的 ImportError，含行号与列名，供日志与排查。
		var ie *excel.ImportError
		if !errors.As(err, &ie) || ie.Total == 0 || ie.Errors[0].Row != 1 || ie.Errors[0].Column != "额外列" {
			t.Errorf("底层 ImportError 丢失：%v", err)
		}
	})

	t.Run("缺少必填列", func(t *testing.T) {
		data := buildGuardWorkbook(t, "数据", [][]any{{"年龄", "加入日期"}, {30, "2026-01-02"}})
		_, err := g.Import(bytes.NewReader(data), opts)
		assertGuardError(t, err, excel.GuardErrInvalidHeader, "表头与导入模板不匹配")
	})
}

func TestImportGuardValidationError200(t *testing.T) {
	g := excel.NewImportGuard[guardRow](1)
	data := buildGuardWorkbook(t, "数据", [][]any{
		guardHeader,
		{"张三", 30, "2026-01-02"},
		{"李四", "abc", "2026-03-04"}, // 单元格解析错误
		{"王五", 20, "2026-05-06"},    // Validate 拒绝
	})
	opts := excel.GuardOptions[guardRow]{
		Validate: func(row int, v *guardRow) error {
			if row == 4 {
				return errors.New("年龄不能小于 18")
			}
			return nil
		},
	}
	_, err := g.Import(bytes.NewReader(data), opts)
	var ve *excel.ValidationError
	if !errors.As(err, &ve) {
		t.Fatalf("error = %v (%T), want *excel.ValidationError", err, err)
	}
	if got := excel.HTTPStatus(err); got != http.StatusOK {
		t.Errorf("HTTPStatus = %d, want 200", got)
	}
	if ve.Total != 2 || len(ve.Errors) != 2 || ve.Truncated {
		t.Errorf("total=%d len=%d truncated=%v, want 2/2/false", ve.Total, len(ve.Errors), ve.Truncated)
	}
	first := ve.Errors[0]
	if first.Row != 3 || first.Column != "年龄" || first.Msg == "" {
		t.Errorf("first cell error = %+v, want 第 3 行 年龄列", first)
	}
	second := ve.Errors[1]
	if second.Row != 4 || second.Column != "" || second.Msg != "年龄不能小于 18" {
		t.Errorf("second cell error = %+v, want 第 4 行 Validate 错误", second)
	}
	// 兼容按 helper 语义判断的调用方。
	var ie *excel.ImportError
	if !errors.As(err, &ie) || ie.Total != 2 {
		t.Errorf("应能还原 helper 的 ImportError：%v", err)
	}
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
			if got := excel.HTTPStatus(tc.err); got != tc.want {
				t.Errorf("HTTPStatus = %d, want %d", got, tc.want)
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

	// 单元级：底层错误保留在错误链上供日志，但不进入用户可见文案。
	_, err := g.Import(iotest.ErrReader(marker), excel.GuardOptions[guardRow]{})
	var ge *excel.GuardError
	if !errors.As(err, &ge) || ge.Kind != excel.GuardErrInternal {
		t.Fatalf("error = %v (%T), want GuardErrInternal", err, err)
	}
	if !errors.Is(err, marker) {
		t.Errorf("底层错误应保留供日志与错误链")
	}
	if ge.Msg != "服务器内部错误" || strings.Contains(ge.Msg, "数据库") || strings.Contains(ge.Msg, "postgres") {
		t.Errorf("msg = %q, want 固定文案且不含内部细节", ge.Msg)
	}
	if got := excel.HTTPStatus(err); got != http.StatusInternalServerError {
		t.Errorf("HTTPStatus = %d, want 500", got)
	}

	// HTTP 级：响应体不包含任何内部细节。
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

// TestImportGuardFixedLimits 锁定 guard 传给 helper 的限额值本身：
// guard 的 GuardOptions 没有限额字段，以下断言保证固定值不变。
func TestImportGuardFixedLimits(t *testing.T) {
	g := excel.NewImportGuard[guardRow](1)
	opts := excel.GuardOptions[guardRow]{}

	t.Run("文件恰好 10MB 进入解析", func(t *testing.T) {
		// 恰好 10MB 不应按文件过大拒绝；内容不是 xlsx，应归类为坏工作簿。
		// 若 guard 传给 helper 的 MaxBytes 小于 10MB，这里会错误地返回 ErrFileTooLarge。
		_, err := g.Import(bytes.NewReader(make([]byte, excel.DefaultMaxBytes)), opts)
		if errors.Is(err, excel.ErrFileTooLarge) {
			t.Fatalf("恰好 10MB 不应按文件过大拒绝，got %v", err)
		}
		if !errors.Is(err, excel.ErrInvalidWorkbook) {
			t.Fatalf("error = %v, want ErrInvalidWorkbook", err)
		}
	})

	t.Run("文件超过 10MB 拒绝", func(t *testing.T) {
		_, err := g.Import(bytes.NewReader(make([]byte, excel.DefaultMaxBytes+1)), opts)
		assertGuardError(t, err, excel.GuardErrFileTooLarge, "文件超过大小上限")
	})

	t.Run("数据恰好 5000 行接受", func(t *testing.T) {
		rows := [][]any{guardHeader}
		for i := 0; i < excel.DefaultMaxRows; i++ {
			rows = append(rows, []any{"客户", 30, "2026-01-02"})
		}
		data := buildGuardWorkbook(t, "数据", rows)
		items, err := g.Import(bytes.NewReader(data), opts)
		if err != nil {
			t.Fatalf("Import: %v", err)
		}
		if len(items) != excel.DefaultMaxRows {
			t.Fatalf("got %d rows, want %d", len(items), excel.DefaultMaxRows)
		}
	})

	t.Run("数据超过 5000 行拒绝", func(t *testing.T) {
		rows := [][]any{guardHeader}
		for i := 0; i <= excel.DefaultMaxRows; i++ {
			rows = append(rows, []any{"客户", 30, "2026-01-02"})
		}
		data := buildGuardWorkbook(t, "数据", rows)
		_, err := g.Import(bytes.NewReader(data), opts)
		assertGuardError(t, err, excel.GuardErrTooManyRows, "数据超过行数上限")
	})

	t.Run("明细错误最多 100 条并截断", func(t *testing.T) {
		rows := [][]any{guardHeader}
		for i := 0; i < 150; i++ {
			rows = append(rows, []any{"客户", "abc", "2026-01-02"}) // 年龄非法
		}
		data := buildGuardWorkbook(t, "数据", rows)
		_, err := g.Import(bytes.NewReader(data), opts)
		var ve *excel.ValidationError
		if !errors.As(err, &ve) {
			t.Fatalf("error = %v (%T), want *excel.ValidationError", err, err)
		}
		if ve.Total != 150 {
			t.Errorf("total = %d, want 150（真实总数不被截断）", ve.Total)
		}
		if len(ve.Errors) != excel.DefaultMaxErrors {
			t.Errorf("明细 = %d 条, want %d", len(ve.Errors), excel.DefaultMaxErrors)
		}
		if !ve.Truncated {
			t.Errorf("truncated = false, want true")
		}
	})
}
