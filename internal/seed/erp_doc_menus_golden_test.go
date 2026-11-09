package seed

import (
	"bufio"
	"cmp"
	"encoding/csv"
	"flag"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"testing"

	sysmodel "github.com/go-quick-code/go-admin-pro/internal/module/system/model"
)

// goldenUpdate 为 true 时把当前输出写回 golden 文件（快照采集，仅人工执行）。
var goldenUpdate = flag.Bool("golden-update", false, "重写 ERP 单据段 golden CSV")

// TestErpDocMenusGolden 是 ERP 单据段菜单种子的行为快照（issue #125 S3）：
// erpDocMenus() 产出的 12 菜单 + 60 按钮（72 行）逐字段固化为
// testdata/erp_doc_menus.golden.csv，seed 侧任何改动都必须让快照逐字段相等，
// 保证存量库与新建库的菜单完全一致。行按 ID 排序；字段为
// id,parent,name,path,component,permission,icon,sort,type。
func TestErpDocMenusGolden(t *testing.T) {
	rows := erpDocMenus()
	if len(rows) != 72 {
		t.Fatalf("单据段应为 72 行（12 菜单 + 60 按钮），实际 %d", len(rows))
	}
	sorted := slices.Clone(rows)
	slices.SortFunc(sorted, func(a, b sysmodel.Menu) int { return cmp.Compare(a.ID, b.ID) })

	var sb strings.Builder
	w := csv.NewWriter(&sb)
	if err := w.Write([]string{"id", "parent", "name", "path", "component", "permission", "icon", "sort", "type"}); err != nil {
		t.Fatalf("写表头: %v", err)
	}
	for _, m := range sorted {
		rec := []string{
			strconv.FormatInt(m.ID, 10),
			strconv.FormatInt(m.ParentID, 10),
			m.Name,
			m.Path,
			m.Component,
			m.Permission,
			m.Icon,
			strconv.Itoa(m.Sort),
			strconv.Itoa(int(m.Type)),
		}
		if err := w.Write(rec); err != nil {
			t.Fatalf("写行 %d: %v", m.ID, err)
		}
	}
	w.Flush()
	got := sb.String()

	golden := filepath.Join("testdata", "erp_doc_menus.golden.csv")
	if *goldenUpdate {
		if err := os.MkdirAll(filepath.Dir(golden), 0o755); err != nil {
			t.Fatalf("建 testdata: %v", err)
		}
		if err := os.WriteFile(golden, []byte(got), 0o644); err != nil {
			t.Fatalf("写 golden: %v", err)
		}
		return
	}

	want, err := os.ReadFile(golden)
	if err != nil {
		t.Fatalf("读 golden（首次采集请运行 go test ./internal/seed -run TestErpDocMenusGolden -golden-update）: %v", err)
	}
	if got != string(want) {
		t.Fatalf("seed 单据段与 golden 不一致（重构必须逐字段保持）：\n--- golden\n+++ 当前\n%s", unifiedDiff(t, string(want), got))
	}
}

// unifiedDiff 输出行级差异，便于在测试失败信息里直接读出哪一列漂移。
func unifiedDiff(t *testing.T, want, got string) string {
	t.Helper()
	scanWant := bufio.NewScanner(strings.NewReader(want))
	scanGot := bufio.NewScanner(strings.NewReader(got))
	var sb strings.Builder
	line := 0
	for {
		wOK := scanWant.Scan()
		gOK := scanGot.Scan()
		if !wOK && !gOK {
			break
		}
		line++
		wLine, gLine := scanWant.Text(), scanGot.Text()
		switch {
		case !wOK:
			sb.WriteString("+" + gLine + "\n")
		case !gOK:
			sb.WriteString("-" + wLine + "\n")
		case wLine != gLine:
			sb.WriteString("-" + wLine + "\n+" + gLine + "\n")
		}
	}
	if sb.Len() == 0 {
		return "（行数一致但内容有不可见差异，长度 want=" + strconv.Itoa(len(want)) + " got=" + strconv.Itoa(len(got)) + "）"
	}
	return sb.String()
}
