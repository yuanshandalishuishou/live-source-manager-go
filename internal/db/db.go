package db

import (
	"database/sql"
	"os"

	_ "github.com/mattn/go-sqlite3"
)

// CreateSchema 创建新数据库文件及表
func CreateSchema(dbPath string) error {
	// 确保目录存在
	if err := os.MkdirAll(dbPath[:len(dbPath)-len("/live-source.db")], 0755); err != nil {
		return err
	}
	db, err := sql.Open("sqlite3", dbPath)
	if err != nil {
		return err
	}
	defer db.Close()
	_, err = db.Exec(SchemaSQL)
	return err
}
