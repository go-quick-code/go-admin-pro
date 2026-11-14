package response_test

import (
	"net/http"
	"net/http/httptest"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/gin-gonic/gin"

	"github.com/go-quick-code/go-admin-pro/internal/pkg/response"
)

// 本文件钉住 SendXLSX 的 HTTP 契约面（spec #144 Testing Decisions）：helper 的产物
// 是响应头与字节，只断言 disposition、content type、状态码与字节，不断言内部拼接。
// 期望字面量取自重构前三份手写实现（crm/erp/pay 的 sendExcel 逐字节相同），是独立
// 于本包代码的已知良好来源；重构后行为必须逐字节等价。

// newXlsxContext 建 gin 测试上下文（gin 标准测试方式），返回上下文与响应记录器。
func newXlsxContext() (*gin.Context, *httptest.ResponseRecorder) {
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest(http.MethodGet, "/export", nil)
	return c, w
}

// asciiDateKey 从 Content-Disposition 的 ASCII 回退名中提取 8 位日期键。
var asciiDateKeyRe = regexp.MustCompile(`filename="[^"]*-(\d{8})\.xlsx"`)

// TestSendXLSXASCIIFallbackName ASCII 回退名进 filename="..."：base-<日期键>.xlsx，
// 本地化名进 filename*（这里一并钉住双文件名形状与三份既有实现逐字节等价）。
func TestSendXLSXASCIIFallbackName(t *testing.T) {
	c, w := newXlsxContext()
	response.SendXLSX(c, time.UTC, "crm-contracts", "合同列表", []byte("xlsx-bytes"))

	disposition := w.Header().Get("Content-Disposition")
	dateKey := time.Now().In(time.UTC).Format("20060102")
	want := `attachment; filename="crm-contracts-` + dateKey + `.xlsx"; filename*=UTF-8''%E5%90%88%E5%90%8C%E5%88%97%E8%A1%A8-` + dateKey + `.xlsx`
	if disposition != want {
		t.Errorf("Content-Disposition 与既有实现不等价：\n got  %s\n want %s", disposition, want)
	}
}

// TestSendXLSXChineseLocalizedRFC5987 中文本地化名经 PathEscape 进 filename* 本地化名
// （RFC 5987），且 disposition 中不残留任何原始中文（浏览器侧解析 RFC 5987 得回中文名）。
func TestSendXLSXChineseLocalizedRFC5987(t *testing.T) {
	c, w := newXlsxContext()
	response.SendXLSX(c, time.UTC, "base", "合同列表", []byte("x"))

	disposition := w.Header().Get("Content-Disposition")
	if !strings.Contains(disposition, "filename*=UTF-8''%E5%90%88%E5%90%8C%E5%88%97%E8%A1%A8-") {
		t.Errorf("filename* 应含 PathEscape 后的中文名（合同列表 → %%E5%%90%%88...），got %s", disposition)
	}
	if regexp.MustCompile(`\p{Han}`).MatchString(disposition) {
		t.Errorf("disposition 不应残留原始中文字符，got %s", disposition)
	}
}

// TestSendXLSXDateKeyFollowsLocation 日期键随 loc 变化：+14 与 -12 两个固定时区
// 相差 26 小时，任何时刻取 Now 的日期必不同；键必须是 8 位数字。
func TestSendXLSXDateKeyFollowsLocation(t *testing.T) {
	getDisposition := func(loc *time.Location) string {
		c, w := newXlsxContext()
		response.SendXLSX(c, loc, "base", "标题", []byte("x"))
		return w.Header().Get("Content-Disposition")
	}
	east := getDisposition(time.FixedZone("+14", 14*3600))
	west := getDisposition(time.FixedZone("-12", -12*3600))

	eastKey := asciiDateKeyRe.FindStringSubmatch(east)
	westKey := asciiDateKeyRe.FindStringSubmatch(west)
	if eastKey == nil || westKey == nil {
		t.Fatalf("disposition 未提取到 8 位日期键：east %q / west %q", east, west)
	}
	if eastKey[1] == westKey[1] {
		t.Errorf("相隔 26 小时的两个时区日期键必须不同：east %s == west %s", eastKey[1], westKey[1])
	}
}

// TestSendXLSXContentTypeAndStatus content type 精确等于 xlsx MIME，状态码 200，
// 字节原样写回（200 写回字节是导出下载契约的一半）。
func TestSendXLSXContentTypeAndStatus(t *testing.T) {
	c, w := newXlsxContext()
	response.SendXLSX(c, time.UTC, "base", "标题", []byte("xlsx-bytes"))

	if got := w.Header().Get("Content-Type"); got != "application/vnd.openxmlformats-officedocument.spreadsheetml.sheet" {
		t.Errorf("Content-Type 精确等于 xlsx MIME，got %q", got)
	}
	if w.Code != http.StatusOK {
		t.Errorf("状态码应为 200，got %d", w.Code)
	}
	if got := w.Body.String(); got != "xlsx-bytes" {
		t.Errorf("响应体应原样写回字节，got %q", got)
	}
}

// TestSendXLSXControlCharsEscaped title 含 \r\n 时被 PathEscape 成 %0D%0A，
// disposition 中无原始 CRLF（header 注入防护）。
func TestSendXLSXControlCharsEscaped(t *testing.T) {
	c, w := newXlsxContext()
	response.SendXLSX(c, time.UTC, "base", "标题\r\nInjected: yes", []byte("x"))

	disposition := w.Header().Get("Content-Disposition")
	if !strings.Contains(disposition, "%0D%0A") {
		t.Errorf("CRLF 应被转义为 %%0D%%0A，got %s", disposition)
	}
	if strings.ContainsAny(disposition, "\r\n") {
		t.Errorf("disposition 不应含原始 CRLF，got %q", disposition)
	}
}

// TestSendXLSXNilLocationPanics loc == nil 直接 panic（fail-closed）：装配期编程
// 错误显性化，而非静默用错时区（正常运行不可达——应用时区启动校验保证非 nil）。
func TestSendXLSXNilLocationPanics(t *testing.T) {
	defer func() {
		if recover() == nil {
			t.Fatal("loc == nil 必须 panic（fail-closed，装配期编程错误显性化）")
		}
	}()
	c, _ := newXlsxContext()
	response.SendXLSX(c, nil, "base", "标题", []byte("x"))
}
