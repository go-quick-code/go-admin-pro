package codegen

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
)

// WriteFiles 把 GenerateEntity 的产物落盘（cmd/codegen CLI 与测试共用）。
// 相对路径按 / 分隔转换为宿主路径；默认拒绝覆盖已存在文件（防误伤已有代码），
// overwrite=true 解锁。返回按文件名排序的已写入路径（相对 outDir）。
func WriteFiles(outDir string, files map[string]string, overwrite bool) ([]string, error) {
	names := make([]string, 0, len(files))
	for name := range files {
		names = append(names, name)
	}
	sort.Strings(names)

	for _, name := range names {
		target := filepath.Join(outDir, filepath.FromSlash(name))
		if _, err := os.Stat(target); err == nil && !overwrite {
			return nil, fmt.Errorf("文件已存在：%s（确认覆盖请加 -overwrite）", target)
		}
		if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
			return nil, err
		}
		if err := os.WriteFile(target, []byte(files[name]), 0o644); err != nil {
			return nil, err
		}
	}
	return names, nil
}

// sortedFileNames 返回文件名升序（zip 条目顺序稳定，便于比对与 diff）。
func sortedFileNames(files map[string]string) []string {
	names := make([]string, 0, len(files))
	for name := range files {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}
