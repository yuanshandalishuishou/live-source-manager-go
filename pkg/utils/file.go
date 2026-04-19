package utils

import (
	"io"
	"os"
	"path/filepath"
)

// EnsureDir 确保目录存在，如果不存在则创建。
func EnsureDir(path string) error {
	return os.MkdirAll(path, 0755)
}

// AtomicWriteFile 原子写入文件：先写入临时文件，再重命名。
func AtomicWriteFile(path string, data []byte) error {
	tmpPath := path + ".tmp"
	if err := os.WriteFile(tmpPath, data, 0644); err != nil {
		return err
	}
	return os.Rename(tmpPath, path)
}

// CopyFile 复制文件从 src 到 dst。
func CopyFile(src, dst string) error {
	sourceFile, err := os.Open(src)
	if err != nil {
		return err
	}
	defer sourceFile.Close()

	destFile, err := os.Create(dst)
	if err != nil {
		return err
	}
	defer destFile.Close()

	_, err = io.Copy(destFile, sourceFile)
	return err
}

// GetFileSize 获取文件大小（字节）。
func GetFileSize(path string) (int64, error) {
	info, err := os.Stat(path)
	if err != nil {
		return 0, err
	}
	return info.Size(), nil
}
