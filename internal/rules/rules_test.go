package rules

import (
	"path/filepath"
	"testing"

	"live-source-manager-go/internal/config"
	"live-source-manager-go/internal/db"
	"live-source-manager-go/internal/types"
)

func TestClassifyEmptyDictionary(t *testing.T) {
	dir := t.TempDir()
	conn, err := db.Open(filepath.Join(dir, "app.db"))
	if err != nil {
		t.Fatalf("db.Open: %v", err)
	}
	defer conn.Close()
	if _, err := db.SeedDefaults(conn, config.DefaultValues()); err != nil {
		t.Fatalf("SeedDefaults: %v", err)
	}

	eng := NewEngine(conn)
	chs := []types.Channel{
		{ID: "1", Name: "CCTV-1", URL: "http://example.com/1.m3u8"},
		{ID: "2", Name: "Hunan TV", URL: "http://example.com/2.m3u8"},
	}
	if err := eng.Classify(chs); err != nil {
		t.Fatalf("Classify error: %v", err)
	}
	for _, c := range chs {
		if c.Categories == nil {
			t.Errorf("channel %q should have a categories map", c.Name)
		}
	}
}

func TestLoadRulesNoError(t *testing.T) {
	dir := t.TempDir()
	conn, err := db.Open(filepath.Join(dir, "app.db"))
	if err != nil {
		t.Fatalf("db.Open: %v", err)
	}
	defer conn.Close()
	if _, err := db.SeedDefaults(conn, config.DefaultValues()); err != nil {
		t.Fatalf("SeedDefaults: %v", err)
	}
	eng := NewEngine(conn)
	if err := eng.LoadRules(); err != nil {
		t.Fatalf("LoadRules error: %v", err)
	}
}
