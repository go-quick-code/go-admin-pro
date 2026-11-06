// Package main 是一次性运维命令：为存量待审合同补建 bpm 审批实例
// （issue #47，docs/oa-mvp-spec.md §4.5）。
//
// 只处理「待审且无运行中实例」的合同；逐合同在事务内锁行复核，重复执行或
// 并发执行都不会重复创建实例。发起人取合同负责人，标题/摘要快照与正常提交一致。
//
// 用法：
//
//	go run ./cmd/backfill-contract-approval -c configs/config.yaml
//
// 或 make backfill-contract-approval。命令复用生产装配（租户插件/系统网关/
// bpm 通知接缝），执行完毕后立即关闭连接；不提供 HTTP 入口，避免运行期
// 被误触发。
package main

import (
	"context"
	"flag"
	"fmt"
	"log/slog"
	"os"

	"github.com/go-quick-code/go-admin-pro/internal/app"
	"github.com/go-quick-code/go-admin-pro/internal/config"
	"github.com/go-quick-code/go-admin-pro/internal/pkg/logger"
)

func main() {
	configPath := flag.String("c", "configs/config.yaml", "配置文件路径")
	flag.Parse()

	cfg, err := config.Load(*configPath)
	if err != nil {
		fmt.Fprintln(os.Stderr, "load config failed:", err)
		os.Exit(1)
	}
	log := logger.Init(cfg.Logger)

	application, err := app.New(cfg, log)
	if err != nil {
		log.Error("startup failed", "err", err)
		os.Exit(1)
	}
	defer func() {
		if err := application.Close(); err != nil {
			log.Error("close app failed", "err", err)
		}
	}()

	result, err := application.CRM.BackfillContractApprovals(context.Background())
	log.Info("contract approval backfill finished",
		"tenants", result.Tenants,
		"scanned", result.Scanned,
		"created", result.Created,
		"skipped", result.Skipped,
		"failed", result.Failed,
	)
	if err != nil {
		slog.Error("contract approval backfill failed", "err", err)
		os.Exit(1)
	}
	fmt.Printf("contract approval backfill: tenants=%d scanned=%d created=%d skipped=%d failed=%d\n",
		result.Tenants, result.Scanned, result.Created, result.Skipped, result.Failed)
}
