package database

import (
	"fmt"
	"log/slog"
	"time"

	"github.com/go-quick-code/go-admin-pro/internal/config"
	"gorm.io/driver/mysql"
	"gorm.io/gorm"
	gormlogger "gorm.io/gorm/logger"
)

// Open 打开 MySQL 连接并返回 *gorm.DB。
func Open(cfg config.DatabaseConfig, log *slog.Logger) (*gorm.DB, error) {
	gormLogLevel := gormlogger.Warn
	db, err := gorm.Open(mysql.Open(cfg.DSN), &gorm.Config{
		Logger: gormlogger.Default.LogMode(gormLogLevel),
		NowFunc: func() time.Time {
			return time.Now().Local()
		},
		DisableForeignKeyConstraintWhenMigrating: true,
	})
	if err != nil {
		return nil, fmt.Errorf("open mysql: %w", err)
	}

	sqlDB, err := db.DB()
	if err != nil {
		return nil, fmt.Errorf("get sql db: %w", err)
	}
	if cfg.MaxIdleConns > 0 {
		sqlDB.SetMaxIdleConns(cfg.MaxIdleConns)
	}
	if cfg.MaxOpenConns > 0 {
		sqlDB.SetMaxOpenConns(cfg.MaxOpenConns)
	}
	sqlDB.SetConnMaxLifetime(time.Hour)

	if err := sqlDB.Ping(); err != nil {
		return nil, fmt.Errorf("ping mysql: %w", err)
	}
	log.Info("database connected")
	return db, nil
}
