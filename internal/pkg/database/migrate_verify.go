package database

import (
	"context"
	"database/sql"
	"fmt"

	"github.com/pressly/goose/v3"
)

// MigrationRoundtrip 记录一次迁移往返验证中各阶段建出的业务表数量。
type MigrationRoundtrip struct {
	TablesAfterUp      int
	TablesAfterReset   int
	TablesAfterUpAgain int
}

// VerifyMigrationRoundtrip 在空库上执行 up → reset → up，校验迁移可往返。
// 为避免误删数据，数据库已有表或已应用版本时直接拒绝执行。
func VerifyMigrationRoundtrip(ctx context.Context, db *sql.DB, expectTables int) (MigrationRoundtrip, error) {
	var result MigrationRoundtrip
	if err := initGoose(); err != nil {
		return result, err
	}

	tables, err := countBusinessTables(ctx, db)
	if err != nil {
		return result, err
	}
	if tables > 0 {
		return result, fmt.Errorf("database already has %d tables; verify requires an empty disposable database (drop/recreate it and retry)", tables)
	}
	applied, err := appliedVersions(ctx, db)
	if err != nil {
		return result, err
	}
	if len(applied) > 0 {
		return result, fmt.Errorf("database already has applied migrations %v; verify requires an empty disposable database (drop/recreate it and retry)", applied)
	}

	if err := MigrateUp(ctx, db); err != nil {
		return result, err
	}
	result.TablesAfterUp, err = countBusinessTables(ctx, db)
	if err != nil {
		return result, err
	}
	if result.TablesAfterUp == 0 {
		return result, fmt.Errorf("migrate up created no tables")
	}
	if expectTables > 0 && result.TablesAfterUp != expectTables {
		return result, fmt.Errorf("migrate up created %d tables, expected %d", result.TablesAfterUp, expectTables)
	}
	if err := VerifyMigrationVersion(ctx, db); err != nil {
		return result, err
	}

	if err := MigrateReset(ctx, db); err != nil {
		return result, err
	}
	result.TablesAfterReset, err = countBusinessTables(ctx, db)
	if err != nil {
		return result, err
	}
	if result.TablesAfterReset != 0 {
		return result, fmt.Errorf("migrate reset left %d tables", result.TablesAfterReset)
	}

	if err := MigrateUp(ctx, db); err != nil {
		return result, err
	}
	result.TablesAfterUpAgain, err = countBusinessTables(ctx, db)
	if err != nil {
		return result, err
	}
	if result.TablesAfterUpAgain != result.TablesAfterUp {
		return result, fmt.Errorf("second migrate up created %d tables, first created %d", result.TablesAfterUpAgain, result.TablesAfterUp)
	}
	if err := VerifyMigrationVersion(ctx, db); err != nil {
		return result, err
	}
	return result, nil
}

func countBusinessTables(ctx context.Context, db *sql.DB) (int, error) {
	var count int
	err := db.QueryRowContext(ctx,
		"SELECT COUNT(*) FROM information_schema.tables WHERE table_schema = DATABASE() AND table_name <> ?",
		goose.TableName(),
	).Scan(&count)
	return count, err
}
