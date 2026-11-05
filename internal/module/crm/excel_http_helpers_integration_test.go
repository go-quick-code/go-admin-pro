//go:build integration

// Excel 导入导出 HTTP 集成测试的公共助手（issue #20）：
// 构造 xlsx、multipart 上传、下载后解析工作簿与数据权限场景的用户/角色装配。
package crm_test

import (
	"bytes"
	"encoding/json"
	"io"
	"mime/multipart"
	"net/http"
	"strings"
	"testing"

	"github.com/lautung/go-admin-pro/internal/pkg/excel"
	"github.com/lautung/go-admin-pro/internal/testutil"
	"github.com/xuri/excelize/v2"
)

// 导入/导出按钮的权限码段（issue #20：4160–4166）；菜单 ID 常量复用各实体测试文件
// （crmCustomerMenuID/crmProductMenuID/crmContractMenuID/crmReceivableMenuID）。
const (
	btnCustomerImport   = int64(4160)
	btnCustomerExport   = int64(4161)
	btnContactImport    = int64(4162)
	btnProductImport    = int64(4163)
	btnProductExport    = int64(4164)
	btnContractExport   = int64(4165)
	btnReceivableExport = int64(4166)
)

// excelSheetName 是导入模板与导出的数据工作表名（excel.DefaultSheetName）。
const excelSheetName = "数据"

// buildWorkbook 生成只含「数据」工作表的内存 xlsx：第一行表头，后续每行一条数据。
func buildWorkbook(t *testing.T, header []string, rows [][]any) []byte {
	t.Helper()
	f := excelize.NewFile()
	defer f.Close()
	if err := f.SetSheetName("Sheet1", excelSheetName); err != nil {
		t.Fatalf("重命名工作表: %v", err)
	}
	all := make([][]any, 0, len(rows)+1)
	all = append(all, toAnyRow(header))
	all = append(all, rows...)
	for i, row := range all {
		cell, err := excelize.CoordinatesToCellName(1, i+1)
		if err != nil {
			t.Fatalf("计算单元格坐标: %v", err)
		}
		if err := f.SetSheetRow(excelSheetName, cell, &row); err != nil {
			t.Fatalf("写入第 %d 行: %v", i+1, err)
		}
	}
	buf, err := f.WriteToBuffer()
	if err != nil {
		t.Fatalf("生成工作簿: %v", err)
	}
	return buf.Bytes()
}

func toAnyRow(row []string) []any {
	out := make([]any, len(row))
	for i, v := range row {
		out[i] = v
	}
	return out
}

// uploadExcel 以 multipart/form-data（字段名 file）上传 xlsx 到指定路径，
// 返回 HTTP 状态与统一响应信封。
func uploadExcel(t *testing.T, env *testutil.IntegrationEnv, token, path, filename string, content []byte) (int, testutil.APIResponse) {
	t.Helper()
	var buf bytes.Buffer
	w := multipart.NewWriter(&buf)
	part, err := w.CreateFormFile("file", filename)
	if err != nil {
		t.Fatalf("构造 multipart 文件字段: %v", err)
	}
	if _, err := part.Write(content); err != nil {
		t.Fatalf("写入 multipart 内容: %v", err)
	}
	if err := w.Close(); err != nil {
		t.Fatalf("关闭 multipart writer: %v", err)
	}

	req, err := http.NewRequest(http.MethodPost, env.APIPrefixURL(path), &buf)
	if err != nil {
		t.Fatalf("构造上传请求: %v", err)
	}
	req.Header.Set("Content-Type", w.FormDataContentType())
	req.Header.Set("Authorization", "Bearer "+token)

	resp, err := env.Server.Client().Do(req)
	if err != nil {
		t.Fatalf("上传 Excel: %v", err)
	}
	defer resp.Body.Close()
	var api testutil.APIResponse
	if err := json.NewDecoder(resp.Body).Decode(&api); err != nil {
		t.Fatalf("解析上传响应: %v", err)
	}
	return resp.StatusCode, api
}

// downloadExcel 请求模板/导出接口，断言 xlsx 二进制与附件头，返回解析后的工作簿。
func downloadExcel(t *testing.T, env *testutil.IntegrationEnv, token, path string) *excelize.File {
	t.Helper()
	req, err := http.NewRequest(http.MethodGet, env.APIPrefixURL(path), nil)
	if err != nil {
		t.Fatalf("构造下载请求: %v", err)
	}
	req.Header.Set("Authorization", "Bearer "+token)
	resp, err := env.Server.Client().Do(req)
	if err != nil {
		t.Fatalf("下载 Excel: %v", err)
	}
	defer resp.Body.Close()
	raw, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("读取 Excel 响应: %v", err)
	}
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("GET %s: HTTP %d body=%s，期望 200", path, resp.StatusCode, string(raw))
	}
	disposition := resp.Header.Get("Content-Disposition")
	if !strings.HasPrefix(disposition, "attachment") || !strings.Contains(disposition, ".xlsx") {
		t.Fatalf("GET %s: Content-Disposition=%q，期望 attachment 且带 .xlsx 文件名", path, disposition)
	}
	if ct := resp.Header.Get("Content-Type"); !strings.Contains(ct, "spreadsheetml") {
		t.Fatalf("GET %s: Content-Type=%q，期望 xlsx MIME 类型", path, ct)
	}
	f, err := excelize.OpenReader(bytes.NewReader(raw))
	if err != nil {
		t.Fatalf("解析下载的 xlsx: %v", err)
	}
	t.Cleanup(func() { f.Close() })
	return f
}

// sheetRows 返回指定工作表的全部行（默认取「数据」）。
func sheetRows(t *testing.T, f *excelize.File, sheet string) [][]string {
	t.Helper()
	if sheet == "" {
		sheet = excelSheetName
	}
	rows, err := f.GetRows(sheet)
	if err != nil {
		t.Fatalf("读取工作表 %q: %v", sheet, err)
	}
	return rows
}

// assertHeader 断言第一行表头与期望完全一致。
func assertHeader(t *testing.T, f *excelize.File, want []string) {
	t.Helper()
	rows := sheetRows(t, f, "")
	if len(rows) == 0 {
		t.Fatal("工作表缺少表头行")
	}
	got := rows[0]
	if len(got) != len(want) {
		t.Fatalf("表头列数=%d（%v），期望 %d（%v）", len(got), got, len(want), want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("第 %d 列表头=%q，期望 %q（完整表头 %v）", i+1, got[i], want[i], got)
		}
	}
}

// assertImportValidation 断言导入返回 HTTP 200 + 非 0 业务码 + 行级明细。
func assertImportValidation(t *testing.T, status int, api testutil.APIResponse, wantRow int, wantColumn string) {
	t.Helper()
	if status != http.StatusOK {
		t.Fatalf("校验失败的导入应返回 HTTP 200，实际 HTTP %d code %d msg %q", status, api.Code, api.Msg)
	}
	if api.Code == 0 {
		t.Fatalf("校验失败的业务码不应为 0（data=%s）", string(api.Data))
	}
	var result struct {
		Errors    []excel.CellError `json:"errors"`
		Total     int               `json:"total"`
		Truncated bool              `json:"truncated"`
	}
	if err := json.Unmarshal(api.Data, &result); err != nil {
		t.Fatalf("解析校验失败明细: %v（data=%s）", err, string(api.Data))
	}
	if result.Total == 0 || len(result.Errors) == 0 {
		t.Fatalf("校验失败应返回行级明细，实际 total=%d errors=%+v", result.Total, result.Errors)
	}
	for _, item := range result.Errors {
		if item.Row == wantRow && item.Column == wantColumn {
			return
		}
	}
	t.Fatalf("期望第 %d 行 [%s] 的错误，实际 %+v", wantRow, wantColumn, result.Errors)
}

// importCount 解析成功响应里的 data.count。
func importCount(t *testing.T, api testutil.APIResponse) int {
	t.Helper()
	var result struct {
		Count int `json:"count"`
	}
	if err := json.Unmarshal(api.Data, &result); err != nil {
		t.Fatalf("解析导入成功响应: %v（data=%s）", err, string(api.Data))
	}
	return result.Count
}

// cellRaw 读取单元格原始值（原生日期为序列号，金额按写入类型）。
func cellRaw(t *testing.T, f *excelize.File, sheet, cell string) string {
	t.Helper()
	if sheet == "" {
		sheet = excelSheetName
	}
	value, err := f.GetCellValue(sheet, cell, excelize.Options{RawCellValue: true})
	if err != nil {
		t.Fatalf("读取单元格 %s!%s: %v", sheet, cell, err)
	}
	return value
}

// excelizeCellTypeNumber 暴露 excelize 的数字单元格类型，避免测试文件重复导入。
func excelizeCellTypeNumber() excelize.CellType { return excelize.CellTypeNumber }

// newExcelRole 创建绑定指定菜单/按钮码、数据范围的角色并创建用户，返回登录 token。
func newExcelRole(t *testing.T, env *testutil.IntegrationEnv, admin, roleCode, username string, dataScope int, menuIDs []int64, deptID int64) string {
	t.Helper()
	roleID := createRoleByAPI(t, env, admin, map[string]any{
		"name": username + "角色", "code": roleCode, "dataScope": dataScope,
		"menuIds": menuIDs,
	})
	createUserByAPI(t, env, admin, username, deptID, []int64{roleID})
	return env.Login(t, username, "123456")
}

// customerImportPayload 把客户导入行的文本列按模板顺序组装；status 传 nil 表示留空。
func customerImportPayload(name, industry, level, source, phone, website, address string, status any, remark string) []any {
	return []any{name, industry, level, source, phone, website, address, status, remark}
}

// customerImportHeader 与 dto.CustomerImportRow 的表头一致。
var customerImportHeader = []string{"客户名称", "行业", "级别", "来源", "联系电话", "网址", "地址", "状态", "备注"}

// contactImportHeader 与 dto.ContactImportRow 的表头一致。
var contactImportHeader = []string{"客户编号", "姓名", "职位", "手机", "邮箱", "性别", "决策角色", "备注"}

// productImportHeader 与 dto.ProductImportRow 的表头一致。
var productImportHeader = []string{"产品名称", "产品编码", "分类名称", "单位", "价格", "状态", "描述"}

// 表头断言用常量（导出与导入模板共用，漂移时测试直接指出列变化）。
var (
	customerTemplateHeader = customerImportHeader
	customerExportHeader   = []string{"客户编号", "客户名称", "状态", "行业", "级别", "来源", "联系电话", "网址", "地址", "负责人", "部门", "备注", "创建时间"}
	contactTemplateHeader  = contactImportHeader
	productTemplateHeader  = productImportHeader
	productExportHeader    = []string{"产品名称", "产品编码", "分类名称", "单位", "价格", "状态", "描述", "创建时间"}
	contractExportHeader   = []string{"合同编号", "合同名称", "所属客户", "合同金额", "状态", "签订日期", "生效日期", "失效日期", "负责人", "部门", "备注", "创建时间"}
	receivableExportHeader = []string{"合同编号", "合同名称", "所属客户", "期次", "合同金额", "计划金额", "已收金额", "到期日", "状态", "逾期", "备注"}
)
