package database

import (
	"context"
	"database/sql"
	"fmt"
	"slices"

	_ "github.com/go-sql-driver/mysql"
	"github.com/lautung/go-admin-pro/internal/config"
	"github.com/lautung/go-admin-pro/migrations"
	"github.com/pressly/goose/v3"
)

// migrationsDir 是内嵌文件系统中的迁移目录（文件位于 FS 根目录）。
const migrationsDir = "."

// initGoose 配置 goose 的全局状态：使用内嵌迁移、MySQL 方言、强制顺序执行。
func initGoose() error {
	goose.SetBaseFS(migrations.FS)
	goose.SetSequential(true)
	return goose.SetDialect("mysql")
}

// OpenSQL 打开 database/sql 连接，供迁移命令使用。
func OpenSQL(cfg config.DatabaseConfig) (*sql.DB, error) {
	db, err := sql.Open("mysql", cfg.DSN)
	if err != nil {
		return nil, fmt.Errorf("open mysql: %w", err)
	}
	if err := db.Ping(); err != nil {
		_ = db.Close()
		return nil, fmt.Errorf("ping mysql: %w", err)
	}
	return db, nil
}

// MigrateUp 应用所有未执行的迁移。
func MigrateUp(ctx context.Context, db *sql.DB) error {
	if err := initGoose(); err != nil {
		return err
	}
	if err := goose.UpContext(ctx, db, migrationsDir); err != nil {
		return fmt.Errorf("goose up: %w", err)
	}
	return nil
}

// MigrateDown 回滚一个迁移版本。
func MigrateDown(ctx context.Context, db *sql.DB) error {
	if err := initGoose(); err != nil {
		return err
	}
	if err := goose.DownContext(ctx, db, migrationsDir); err != nil {
		return fmt.Errorf("goose down: %w", err)
	}
	return nil
}

// MigrateReset 回滚所有迁移版本。
func MigrateReset(ctx context.Context, db *sql.DB) error {
	if err := initGoose(); err != nil {
		return err
	}
	if err := goose.ResetContext(ctx, db, migrationsDir); err != nil {
		return fmt.Errorf("goose reset: %w", err)
	}
	return nil
}

// MigrateStatus 打印每个迁移版本的应用状态。
func MigrateStatus(ctx context.Context, db *sql.DB) error {
	if err := initGoose(); err != nil {
		return err
	}
	if err := goose.StatusContext(ctx, db, migrationsDir); err != nil {
		return fmt.Errorf("goose status: %w", err)
	}
	return nil
}

// MigrateStamp 把已有库标记为指定迁移版本，不执行任何 DDL。
// 仅用于结构已存在但没有 goose 版本记录的库（例如旧 AutoMigrate 建的库）；
// 它会检查基线 17 张表存在，并按 <= version 回填版本记录，stamp 后应执行 migrate up。
func MigrateStamp(ctx context.Context, db *sql.DB, version int64) error {
	if err := initGoose(); err != nil {
		return err
	}
	versions, err := embeddedVersions()
	if err != nil {
		return err
	}
	if !slices.Contains(versions, version) {
		return fmt.Errorf("version %d is not an embedded migration (known versions: %v)", version, versions)
	}
	if baseline := versions[0]; version != baseline {
		return fmt.Errorf("only the baseline version (%d) can be stamped; apply later versions with migrate up", baseline)
	}

	current, err := goose.EnsureDBVersionContext(ctx, db)
	if err != nil {
		return fmt.Errorf("read migration version: %w", err)
	}
	if current == version {
		fmt.Printf("database is already at version %d\n", version)
		return nil
	}
	if current != 0 {
		return fmt.Errorf("database already has applied version %d; refusing to stamp version %d over existing history", current, version)
	}

	var tableCount int
	if err := db.QueryRowContext(ctx,
		"SELECT COUNT(*) FROM information_schema.tables WHERE table_schema = DATABASE() AND table_name <> ?",
		goose.TableName(),
	).Scan(&tableCount); err != nil {
		return fmt.Errorf("inspect database tables: %w", err)
	}
	if tableCount == 0 {
		return fmt.Errorf("refusing to stamp an empty database; run migrate-up instead")
	}
	missing, err := missingBaselineTables(ctx, db)
	if err != nil {
		return err
	}
	if len(missing) > 0 {
		return fmt.Errorf("missing baseline tables %v; verify the schema before stamping", missing)
	}

	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin stamp transaction: %w", err)
	}
	defer tx.Rollback()

	query := fmt.Sprintf("INSERT INTO `%s` (version_id, is_applied) VALUES (?, 1)", goose.TableName())
	stamped := make([]int64, 0, len(versions))
	for _, v := range versions {
		if v > version {
			break
		}
		if _, err := tx.ExecContext(ctx, query, v); err != nil {
			return fmt.Errorf("stamp version %d: %w", v, err)
		}
		stamped = append(stamped, v)
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit stamp: %w", err)
	}
	fmt.Printf("database stamped at versions %v; run `make migrate-up` to apply later migrations\n", stamped)
	return nil
}

// baselineTables 是 00001_baseline.sql 建出的 17 张表，用于 stamp 前校验结构。
var baselineTables = []string{
	"system_tenant",
	"system_user",
	"system_role",
	"system_menu",
	"system_dept",
	"system_post",
	"system_user_role",
	"system_user_post",
	"system_role_menu",
	"system_dict_type",
	"system_dict_data",
	"system_login_log",
	"system_operate_log",
	"infra_file",
	"infra_config",
	"infra_job",
	"infra_job_log",
}

func missingBaselineTables(ctx context.Context, db *sql.DB) ([]string, error) {
	var missing []string
	for _, table := range baselineTables {
		var count int
		if err := db.QueryRowContext(ctx,
			"SELECT COUNT(*) FROM information_schema.tables WHERE table_schema = DATABASE() AND table_name = ?",
			table,
		).Scan(&count); err != nil {
			return nil, fmt.Errorf("inspect table %s: %w", table, err)
		}
		if count == 0 {
			missing = append(missing, table)
		}
	}
	return missing, nil
}

// CreateMigration 在磁盘的 migrations 目录下创建新的 SQL 迁移模板（不连数据库）。
func CreateMigration(name string) error {
	if err := initGoose(); err != nil {
		return err
	}
	if err := goose.Create(nil, "migrations", name, "sql"); err != nil {
		return fmt.Errorf("create migration: %w", err)
	}
	return nil
}
