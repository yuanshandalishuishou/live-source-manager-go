// Package db 负责数据库初始化、连接管理以及默认数据填充。
package db

import (
	"database/sql"
	_ "embed"
	"fmt"
	"os"
	"path/filepath"

	_ "github.com/mattn/go-sqlite3"
)

//go:embed schema.sql
var schemaSQL string

// InitDB 初始化数据库。若 dbPath 不存在，则创建并执行 schema.sql。
func InitDB(dbPath string) (*sql.DB, error) {
	// 确保目录存在
	dir := filepath.Dir(dbPath)
	if err := os.MkdirAll(dir, 0755); err != nil {
		return nil, fmt.Errorf("创建数据库目录失败: %w", err)
	}

	// 检查数据库文件是否存在，不存在则创建
	needInit := false
	if _, err := os.Stat(dbPath); os.IsNotExist(err) {
		needInit = true
	}

	db, err := sql.Open("sqlite3", dbPath+"?_foreign_keys=on&_journal_mode=WAL&_synchronous=NORMAL")
	if err != nil {
		return nil, fmt.Errorf("打开数据库失败: %w", err)
	}

	if needInit {
		if _, err := db.Exec(schemaSQL); err != nil {
			return nil, fmt.Errorf("执行建表脚本失败: %w", err)
		}
	}

	// 验证连接
	if err := db.Ping(); err != nil {
		return nil, fmt.Errorf("数据库连接测试失败: %w", err)
	}

	return db, nil
}
