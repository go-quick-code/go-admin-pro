// Package main 是 codegen CLI：按实体元信息 JSON 生成 owner-scoped 实体全栈脚手架。
//
// 用法（#11 批量生成后续 CRM 实体的推荐入口）：
//
//	go run ./cmd/codegen -spec internal/module/codegen/testdata/entity/product.json -out /tmp/gen
//	go run ./cmd/codegen -spec my.json -out gen/ -overwrite
//
// 生成不落库、不改工程文件；产物树（INTEGRATION.md、go/、web/、migrations/、seed/）
// 按 INTEGRATION.md 的接线步骤并入工程。HTTP 端点（POST /infra/codegen/generate-entity）
// 与本命令共用同一生成入口 internal/module/codegen.GenerateEntity。
package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"os"

	"github.com/go-quick-code/go-admin-pro/internal/module/codegen"
)

func main() {
	specPath := flag.String("spec", "", "实体元信息 JSON 文件路径（必填，字段说明见 README codegen 一节）")
	outDir := flag.String("out", "", "产物输出目录（必填，不存在会自动创建）")
	overwrite := flag.Bool("overwrite", false, "允许覆盖输出目录中已存在的文件")
	flag.Parse()

	if err := run(*specPath, *outDir, *overwrite); err != nil {
		fmt.Fprintln(os.Stderr, "错误："+err.Error())
		os.Exit(1)
	}
}

func run(specPath, outDir string, overwrite bool) error {
	if specPath == "" {
		return fmt.Errorf("必须用 -spec 指定实体元信息 JSON 路径")
	}
	if outDir == "" {
		return fmt.Errorf("必须用 -out 指定产物输出目录")
	}
	raw, err := os.ReadFile(specPath)
	if err != nil {
		return fmt.Errorf("读取元信息 %s: %w", specPath, err)
	}
	var spec codegen.EntitySpec
	if err := json.Unmarshal(raw, &spec); err != nil {
		return fmt.Errorf("解析元信息 JSON: %w", err)
	}

	files, err := codegen.GenerateEntity(&spec)
	if err != nil {
		return err
	}
	written, err := codegen.WriteFiles(outDir, files, overwrite)
	if err != nil {
		return err
	}
	fmt.Printf("已生成 %s（%s）脚手架 %d 个文件到 %s：\n", spec.Comment, spec.Entity, len(written), outDir)
	for _, name := range written {
		fmt.Println("  " + name)
	}
	fmt.Println("接线步骤见产物内 INTEGRATION.md。")
	return nil
}
