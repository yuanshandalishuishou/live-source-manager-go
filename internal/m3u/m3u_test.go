package m3u

import (
	"strings"
	"testing"

	"live-source-manager-go/internal/types"
)

func sampleChannels() []types.Channel {
	return []types.Channel{
		{ID: "1", Name: "CCTV-1", URL: "http://example.com/1.m3u8", Status: "success", Categories: map[string]string{"content": "央视"}},
		{ID: "2", Name: "CCTV-2", URL: "http://example.com/2.m3u8", Status: "success", Categories: map[string]string{"content": "央视"}},
		{ID: "3", Name: "Bad", URL: "http://example.com/bad.m3u8", Status: "failed"},
	}
}

func TestGenerateFlat(t *testing.T) {
	out, err := Generate(sampleChannels(), Options{GroupBy: "none", IncludeFailed: false})
	if err != nil {
		t.Fatalf("Generate error: %v", err)
	}
	if !strings.HasPrefix(out, "#EXTM3U") {
		t.Fatal("output must start with #EXTM3U")
	}
	if !strings.Contains(out, "CCTV-1") || !strings.Contains(out, "http://example.com/1.m3u8") {
		t.Fatal("success channel missing from output")
	}
	if strings.Contains(out, "Bad") {
		t.Fatal("failed channel should be excluded when IncludeFailed=false")
	}
}

func TestGenerateIncludeFailed(t *testing.T) {
	out, _ := Generate(sampleChannels(), Options{GroupBy: "none", IncludeFailed: true})
	if !strings.Contains(out, "Bad") {
		t.Fatal("failed channel must appear when IncludeFailed=true")
	}
}

func TestGenerateGroupByCategory(t *testing.T) {
	out, _ := Generate(sampleChannels(), Options{GroupBy: "category", IncludeFailed: false})
	if !strings.Contains(out, "#EXTGRP:央视") {
		t.Fatal("expected #EXTGRP:央视 group header")
	}
}

func TestGenerateBlacklist(t *testing.T) {
	chs := []types.Channel{
		{ID: "1", Name: "Keep", URL: "http://keep.example.com/x.m3u8", Status: "success"},
		{ID: "2", Name: "Drop", URL: "http://drop.example.com/x.m3u8", Status: "success"},
	}
	out, _ := Generate(chs, Options{GroupBy: "none", IncludeFailed: false, Blacklist: []string{"drop.example.com"}})
	if strings.Contains(out, "Drop") {
		t.Fatal("blacklisted host must be dropped")
	}
	if !strings.Contains(out, "Keep") {
		t.Fatal("non-blacklisted host must be kept")
	}
}
