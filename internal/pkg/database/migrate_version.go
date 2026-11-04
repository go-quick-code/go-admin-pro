package database

import (
	"context"
	"database/sql"
	"fmt"
	"math"
	"slices"

	"github.com/pressly/goose/v3"
)

// MigrationVersionMismatchKind 描述数据库版本与二进制期望版本不一致的类型。
type MigrationVersionMismatchKind string

const (
	// MigrationBehind 表示数据库落后于二进制（未迁移或缺少后续版本）。
	MigrationBehind MigrationVersionMismatchKind = "behind"
	// MigrationAhead 表示数据库存在二进制不认识的版本。
	MigrationAhead MigrationVersionMismatchKind = "ahead"
	// MigrationDiverged 表示已应用版本不是期望版本的前缀，历史被改动过。
	MigrationDiverged MigrationVersionMismatchKind = "diverged"
)

// MigrationVersionError 表示数据库迁移版本与当前二进制不匹配。
type MigrationVersionError struct {
	Applied  []int64
	Expected []int64
	Kind     MigrationVersionMismatchKind
}

func (e *MigrationVersionError) Error() string {
	current := lastVersion(e.Applied)
	latest := lastVersion(e.Expected)

	switch e.Kind {
	case MigrationBehind:
		if len(e.Applied) == 0 {
			return fmt.Sprintf("database is not migrated: no applied migrations, binary expects version %d; run `make migrate-up` before starting", latest)
		}
		return fmt.Sprintf("database migration is behind: applied version %d, binary expects %d; run `make migrate-up` before starting", current, latest)
	case MigrationAhead:
		return fmt.Sprintf("database migration is ahead: applied version %d, binary only knows %d; deploy the matching binary or roll the database back", current, latest)
	default:
		return fmt.Sprintf("database migration history diverged: applied versions %v, binary expects %v; reconcile the goose_db_version table", e.Applied, e.Expected)
	}
}

func lastVersion(versions []int64) int64 {
	if len(versions) == 0 {
		return 0
	}
	return versions[len(versions)-1]
}

// checkMigrationVersion 比较已应用版本与二进制内嵌版本，只做纯计算。
// 两个切片都必须按版本号升序。
func checkMigrationVersion(applied, expected []int64) error {
	if len(expected) == 0 {
		return fmt.Errorf("binary embeds no migrations")
	}
	if slices.Equal(applied, expected) {
		return nil
	}
	if len(applied) < len(expected) && slices.Equal(applied, expected[:len(applied)]) {
		return &MigrationVersionError{Applied: applied, Expected: expected, Kind: MigrationBehind}
	}
	if len(applied) > len(expected) && slices.Equal(expected, applied[:len(expected)]) {
		return &MigrationVersionError{Applied: applied, Expected: expected, Kind: MigrationAhead}
	}
	return &MigrationVersionError{Applied: applied, Expected: expected, Kind: MigrationDiverged}
}

// VerifyMigrationVersion 校验数据库迁移版本与二进制内嵌版本一致。
// 它只读 goose_db_version，不会创建表或执行迁移；版本表不存在时视为未迁移。
func VerifyMigrationVersion(ctx context.Context, db *sql.DB) error {
	if err := initGoose(); err != nil {
		return err
	}
	expected, err := embeddedVersions()
	if err != nil {
		return err
	}
	applied, err := appliedVersions(ctx, db)
	if err != nil {
		return fmt.Errorf("read migration version: %w", err)
	}
	return checkMigrationVersion(applied, expected)
}

// embeddedVersions 返回内嵌迁移的版本号，升序。
func embeddedVersions() ([]int64, error) {
	if err := initGoose(); err != nil {
		return nil, err
	}
	collected, err := goose.CollectMigrations(migrationsDir, 0, math.MaxInt64)
	if err != nil {
		return nil, fmt.Errorf("collect migrations: %w", err)
	}
	versions := make([]int64, 0, len(collected))
	for _, m := range collected {
		versions = append(versions, m.Version)
	}
	slices.Sort(versions)
	return versions, nil
}

// appliedVersions 返回数据库中已应用的迁移版本，升序。
// goose 会在版本表中保留一条 version_id=0 的初始化记录，这里过滤掉。
func appliedVersions(ctx context.Context, db *sql.DB) ([]int64, error) {
	var tableCount int
	err := db.QueryRowContext(ctx,
		"SELECT COUNT(*) FROM information_schema.tables WHERE table_schema = DATABASE() AND table_name = ?",
		goose.TableName(),
	).Scan(&tableCount)
	if err != nil {
		return nil, err
	}
	if tableCount == 0 {
		return nil, nil
	}

	rows, err := db.QueryContext(ctx,
		fmt.Sprintf("SELECT version_id FROM `%s` WHERE is_applied = 1 AND version_id > 0 ORDER BY version_id", goose.TableName()),
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var versions []int64
	for rows.Next() {
		var v int64
		if err := rows.Scan(&v); err != nil {
			return nil, err
		}
		versions = append(versions, v)
	}
	return versions, rows.Err()
}
