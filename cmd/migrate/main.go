// Command migrate 执行 goose 版本化迁移。
//
// 用法：
//
//	go run ./cmd/migrate -c configs/config.yaml up
//	go run ./cmd/migrate -c configs/config.yaml down
//	go run ./cmd/migrate -c configs/config.yaml reset
//	go run ./cmd/migrate -c configs/config.yaml status
//	go run ./cmd/migrate -c configs/config.yaml create add_crm_customer
package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"time"

	"github.com/lautung/go-admin-pro/internal/config"
	"github.com/lautung/go-admin-pro/internal/pkg/database"
)

func main() {
	configPath := flag.String("c", "configs/config.yaml", "配置文件路径")
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

	cfg, err := config.Load(*configPath)
	if err != nil {
		fail(fmt.Errorf("load config: %w", err))
	}
	db, err := database.OpenSQL(cfg.Database)
	if err != nil {
		fail(err)
	}
	defer db.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()

	switch cmd {
	case "up":
		err = database.MigrateUp(ctx, db)
	case "down":
		err = database.MigrateDown(ctx, db)
	case "reset":
		err = database.MigrateReset(ctx, db)
	case "status":
		err = database.MigrateStatus(ctx, db)
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
	fmt.Fprint(os.Stderr, `用法: migrate [-c config.yaml] <command>

命令:
  up        应用所有未执行的迁移
  down      回滚一个迁移版本
  reset     回滚所有迁移版本
  status    查看迁移状态
  create    新建 SQL 迁移模板（不需要数据库）
`)
}

func fail(err error) {
	fmt.Fprintln(os.Stderr, "migrate failed:", err)
	os.Exit(1)
}
