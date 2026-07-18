// Package util provides small shared helpers (hashing, file IO, string utils).
package util

import (
	"crypto/md5"
	"encoding/hex"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"strings"
)

// MD5Hex returns the hex MD5 of s.
func MD5Hex(s string) string {
	sum := md5.Sum([]byte(s))
	return hex.EncodeToString(sum[:])
}

// ChannelID computes the canonical channel ID: md5(name|url)[:12].
func ChannelID(name, url string) string {
	return MD5Hex(name + "|" + url)[:12]
}

// FileID computes the source-file ID: md5(path)[:12].
func FileID(path string) string {
	return MD5Hex(path)[:12]
}

// NormalizePathSeparator unifies back-slashes to forward-slashes (cross-platform consistency).
func NormalizePathSeparator(p string) string {
	return strings.ReplaceAll(p, "\\", "/")
}

// EnsureDir creates a directory (and parents) if missing.
func EnsureDir(dir string) error {
	if dir == "" {
		return nil
	}
	return os.MkdirAll(dir, 0o755)
}

// FileExists reports whether path exists.
func FileExists(path string) bool {
	_, err := os.Stat(path)
	return err == nil
}

// ReadFileString reads a file fully as a string.
func ReadFileString(path string) (string, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return "", err
	}
	return string(b), nil
}

// WriteFileString writes content to path, creating parent dirs.
func WriteFileString(path, content string) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	return os.WriteFile(path, []byte(content), 0o644)
}

// CopyFile copies src to dst, creating parent dirs.
func CopyFile(src, dst string) error {
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()
	if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
		return err
	}
	out, err := os.Create(dst)
	if err != nil {
		return err
	}
	defer out.Close()
	_, err = io.Copy(out, in)
	return err
}

// RemoveFile deletes a file if it exists (ignores missing).
func RemoveFile(path string) error {
	if !FileExists(path) {
		return nil
	}
	return os.Remove(path)
}

// URLToFilename maps a URL to a safe filename under a directory (mirrors SourceManager logic).
func URLToFilename(rawURL string) string {
	clean := rawURL
	if i := strings.Index(clean, "?"); i >= 0 {
		clean = clean[:i]
	}
	filename := clean
	if i := strings.LastIndex(filename, "/"); i >= 0 {
		filename = filename[i+1:]
	}
	if filename == "" || !strings.Contains(filename, ".") {
		hash := MD5Hex(rawURL)[:8]
		filename = "source_" + hash + ".txt"
	}
	re := regexp.MustCompile(`[^\w\-_.]`)
	return re.ReplaceAllString(filename, "_")
}

// SplitLines splits text into non-empty trimmed lines.
func SplitLines(s string) []string {
	var out []string
	for _, line := range strings.Split(s, "\n") {
		line = strings.TrimSpace(line)
		if line != "" {
			out = append(out, line)
		}
	}
	return out
}

// SplitSectionValues splits a multi-line config value into a string slice.
func SplitSectionValues(raw string) []string {
	if strings.TrimSpace(raw) == "" {
		return nil
	}
	return SplitLines(raw)
}

// JoinSectionValues joins a slice into a newline-separated config value.
func JoinSectionValues(vals []string) string {
	return strings.Join(vals, "\n")
}
