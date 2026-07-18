package config

import (
	"database/sql"
	"path/filepath"
	"testing"

	"live-source-manager-go/internal/db"
)

func openTestDB(t *testing.T) *sql.DB {
	t.Helper()
	dir := t.TempDir()
	conn, err := db.Open(filepath.Join(dir, "app.db"))
	if err != nil {
		t.Fatalf("db.Open: %v", err)
	}
	if _, err := db.SeedDefaults(conn, DefaultValues()); err != nil {
		t.Fatalf("SeedDefaults: %v", err)
	}
	t.Cleanup(func() { conn.Close() })
	return conn
}

func TestDefaultValuesNonEmpty(t *testing.T) {
	d := DefaultValues()
	if len(d) == 0 {
		t.Fatal("DefaultValues must not be empty")
	}
	if d["HTTPServer.manager_port"] != "23456" {
		t.Errorf("manager_port default mismatch: %q", d["HTTPServer.manager_port"])
	}
}

func TestGetSeeded(t *testing.T) {
	cfg := New(openTestDB(t))
	if v := cfg.Get("HTTPServer", "manager_port", "9999"); v != "23456" {
		t.Errorf("seeded manager_port should be 23456, got %q", v)
	}
	if v := cfg.GetInt("HTTPServer", "fileshare_port", 0); v != 12345 {
		t.Errorf("seeded fileshare_port should be 12345, got %d", v)
	}
}

func TestGetFallback(t *testing.T) {
	cfg := New(openTestDB(t))
	if v := cfg.Get("Nope", "missing", "fallback"); v != "fallback" {
		t.Errorf("missing key should return fallback, got %q", v)
	}
	if v := cfg.GetInt("Nope", "missing", 7); v != 7 {
		t.Errorf("missing int key should return fallback, got %d", v)
	}
}

func TestSetAndGet(t *testing.T) {
	cfg := New(openTestDB(t))
	cfg.Set("Testing", "timeout", "42")
	if v := cfg.GetInt("Testing", "timeout", 0); v != 42 {
		t.Errorf("Set/Get roundtrip failed, got %d", v)
	}
}
