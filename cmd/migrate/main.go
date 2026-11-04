// Command migrate 执行 goose 版本化迁移。
//
// 用法：
//
//	go run ./cmd/migrate -c configs/config.yaml up
//	go run ./cmd/migrate -c configs/config.yaml down
//	go run ./cmd/migrate -c configs/config.yaml reset
//	go run ./cmd/migrate -c configs/config.yaml status
//	go run ./cmd/migrate -c configs/config.yaml stamp 1
//	go run ./cmd/migrate -c configs/config.yaml create add_crm_customer
package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"strconv"
	"time"

	"github.com/lautung/go-admin-pro/internal/config"
	"github.com/lautung/go-admin-pro/internal/pkg/database"
)

func main() {
	configPath := flag.String("c", "configs/config.yaml", "配置文件路径")
	timeout := flag.Duration("timeout", 30*time.Minute, "迁移超时时间，0 表示不限制")
	flag.Parse()

	args := flag.Args()
	if len(args) == 0 {
		usage()
		os.Exit(2)
	}

	cmd := args[0]
	if cmd == "create" {
		if len(args) < 2 || args[1] == "" {
			fmt.Fprintln(os.Stderr, "用法: migrate create <name>")
			os.Exit(2)
		}
		if err := database.CreateMigration(args[1]); err != nil {
			fail(err)
		}
		return
	}
	if len(args) > 1 && cmd != "stamp" {
		fmt.Fprintf(os.Stderr, "unexpected arguments for %q: %v (flags must come before the command)\n", cmd, args[1:])
		os.Exit(2)
	}

	cfg, err := config.Load(*configPath)
	if err != nil {
		fail(fmt.Errorf("load config: %w", err))
	}
	db, err := database.OpenSQL(cfg.Database)
	if err != nil {
		fail(err)
	}
	defer db.Close()

	ctx := context.Background()
	if *timeout > 0 {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, *timeout)
		defer cancel()
	}

	switch cmd {
	case "up":
		err = database.MigrateUp(ctx, db)
	case "down":
		err = database.MigrateDown(ctx, db)
	case "reset":
		err = database.MigrateReset(ctx, db)
	case "status":
		err = database.MigrateStatus(ctx, db)
	case "stamp":
		if len(args) < 2 {
			fmt.Fprintln(os.Stderr, "用法: migrate stamp <version>")
			os.Exit(2)
		}
		version, parseErr := strconv.ParseInt(args[1], 10, 64)
		if parseErr != nil || version <= 0 {
			fmt.Fprintf(os.Stderr, "无效版本号: %s\n", args[1])
			os.Exit(2)
		}
		err = database.MigrateStamp(ctx, db, version)
	default:
		fmt.Fprintf(os.Stderr, "未知命令: %s\n\n", cmd)
		usage()
		os.Exit(2)
	}
	if err != nil {
		fail(err)
	}
}

func usage() {
	fmt.Fprint(os.Stderr, `用法: migrate [-c config.yaml] [-timeout 30m] <command>

命令:
  up             应用所有未执行的迁移
  down           回滚一个迁移版本
  reset          回滚所有迁移版本
  status         查看迁移状态
  stamp <version> 把已有库标记为指定迁移版本（不执行 DDL）
  create <name>  新建 SQL 迁移模板（不需要数据库）

-timeout 0 表示不限制迁移时长。
`)
}

func fail(err error) {
	fmt.Fprintln(os.Stderr, "migrate failed:", err)
	os.Exit(1)
}
