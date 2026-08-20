package manager

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"live-source-manager-go/internal/config"
	"live-source-manager-go/internal/db"
	"live-source-manager-go/internal/types"
)

func containsFile(files []string, name string) bool {
	for _, f := range files {
		if f == name {
			return true
		}
	}
	return false
}

func readOut(t *testing.T, dir, name string) string {
	t.Helper()
	b, err := os.ReadFile(filepath.Join(dir, name))
	if err != nil {
		t.Fatalf("read output %s: %v", name, err)
	}
	return string(b)
}

// TestGenerateFromTest_WritesOnlySuccessChannels verifies the "测完一键落盘"
// path: a completed realtime session keeps ONLY the channels whose TestResult
// Status == "success", back-fills their perf fields, and writes the multi-file
// playlist set — without re-running the probe. A failed channel must never leak
// into any output file. With the default enable_filter=False and
// separate_ipv4_ipv6=True, the produced files are live.m3u (base),
// live-ipv4.m3u (single-stack, because the only source is IPv4) and
// qualified_live.m3u (== valid when filtering is off).
func TestGenerateFromTest_WritesOnlySuccessChannels(t *testing.T) {
	conn, err := db.Open(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	defer conn.Close()

	cfg := config.New(conn)
	outDir := t.TempDir()
	cfg.Set("Output", "output_dir", outDir)

	mgr := New(conn, cfg)

	ch1 := types.Channel{ID: "c1", Name: "CCTV1", URL: "http://example.com/c1.m3u8"}
	ch2 := types.Channel{ID: "c2", Name: "CCTV2", URL: "http://example.com/c2.m3u8"}
	sess := &RealtimeSession{
		ID:       "sess1",
		channels: []types.Channel{ch1, ch2},
		Progress: types.TestProgress{Status: "done"},
		Results: map[string]types.TestResult{
			"c1": {ID: "c1", Status: "success", ResponseTime: 0.3, DownloadSpeed: 1200, Bitrate: 2000, Resolution: "1920x1080", FPS: 25, HasVideoStream: true},
			"c2": {ID: "c2", Status: "failed", Category: "timeout"},
		},
	}
	mgr.sessions["sess1"] = sess

	files, count, gerr := mgr.GenerateFromTest("sess1")
	if gerr != nil {
		t.Fatalf("GenerateFromTest error: %v", gerr)
	}
	if count != 1 {
		t.Fatalf("expected 1 kept channel, got %d", count)
	}

	// base file
	base := readOut(t, outDir, "live.m3u")
	if !strings.Contains(base, "CCTV1") || !strings.Contains(base, "http://example.com/c1.m3u8") {
		t.Fatalf("success channel missing from live.m3u:\n%s", base)
	}
	if strings.Contains(base, "CCTV2") {
		t.Fatalf("failed channel leaked into live.m3u:\n%s", base)
	}

	// multi-file contract (default separate_ipv4_ipv6=True, filtering off)
	if !containsFile(files, "live.m3u") {
		t.Fatalf("live.m3u missing from files %v", files)
	}
	if !containsFile(files, "live-ipv4.m3u") {
		t.Fatalf("live-ipv4.m3u missing (only IPv4 source present): %v", files)
	}
	if containsFile(files, "live-ipv6.m3u") {
		t.Fatalf("unexpected live-ipv6.m3u (no IPv6 source): %v", files)
	}
	if !containsFile(files, "qualified_live.m3u") {
		t.Fatalf("qualified_live.m3u missing: %v", files)
	}

	// single-stack + qualified also carry the success channel, never the failed one
	ipv4 := readOut(t, outDir, "live-ipv4.m3u")
	if !strings.Contains(ipv4, "CCTV1") || strings.Contains(ipv4, "CCTV2") {
		t.Fatalf("live-ipv4.m3u wrong content:\n%s", ipv4)
	}
	qual := readOut(t, outDir, "qualified_live.m3u")
	if !strings.Contains(qual, "CCTV1") || strings.Contains(qual, "CCTV2") {
		t.Fatalf("qualified_live.m3u wrong content:\n%s", qual)
	}
}

// TestGenerateFromTest_SingleStackAndQualified exercises enable_filter=True with
// a mix of IPv4 / IPv6 sources and a reachable-but-low-quality source:
//   - valid        = all success (a, b, c)
//   - base         = resolution grouping of valid (a, b, c)
//   - qualified    = quality filter of base (a, c) -> b fails min_speed
//   - live-ipv4    = {a, b}, live-ipv6 = {c}
func TestGenerateFromTest_SingleStackAndQualified(t *testing.T) {
	conn, err := db.Open(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	defer conn.Close()

	cfg := config.New(conn)
	outDir := t.TempDir()
	cfg.Set("Output", "output_dir", outDir)
	cfg.Set("Output", "enable_filter", "True")

	mgr := New(conn, cfg)

	a := types.Channel{ID: "a", Name: "A", URL: "http://192.168.1.1/a.m3u8"}
	b := types.Channel{ID: "b", Name: "B", URL: "http://192.168.1.2/b.m3u8"} // low speed
	c := types.Channel{ID: "c", Name: "C", URL: "http://[2001:db8::1]/c.m3u8"}
	sess := &RealtimeSession{
		ID:       "s",
		channels: []types.Channel{a, b, c},
		Progress: types.TestProgress{Status: "done"},
		Results: map[string]types.TestResult{
			"a": {ID: "a", Status: "success", ResponseTime: 0.3, DownloadSpeed: 1200, Bitrate: 2000, Resolution: "1920x1080", HasVideoStream: true},
			"b": {ID: "b", Status: "success", ResponseTime: 1.0, DownloadSpeed: 10, Bitrate: 1000, Resolution: "1280x720", HasVideoStream: true},
			"c": {ID: "c", Status: "success", ResponseTime: 0.5, DownloadSpeed: 800, Bitrate: 1500, Resolution: "1920x1080", HasVideoStream: true},
		},
	}
	mgr.sessions["s"] = sess

	files, count, gerr := mgr.GenerateFromTest("s")
	if gerr != nil {
		t.Fatalf("GenerateFromTest error: %v", gerr)
	}
	if count != 3 {
		t.Fatalf("expected 3 valid channels, got %d", count)
	}

	base := readOut(t, outDir, "live.m3u")
	for _, name := range []string{"A", "B", "C"} {
		if !strings.Contains(base, name) {
			t.Fatalf("live.m3u missing %s:\n%s", name, base)
		}
	}

	ipv4 := readOut(t, outDir, "live-ipv4.m3u")
	if !strings.Contains(ipv4, "A") || !strings.Contains(ipv4, "B") || strings.Contains(ipv4, "C") {
		t.Fatalf("live-ipv4.m3u wrong (expected A,B not C):\n%s", ipv4)
	}
	ipv6 := readOut(t, outDir, "live-ipv6.m3u")
	if !strings.Contains(ipv6, "C") || strings.Contains(ipv6, "A") || strings.Contains(ipv6, "B") {
		t.Fatalf("live-ipv6.m3u wrong (expected C only):\n%s", ipv6)
	}

	// qualified drops B (speed 10 < min_speed 50)
	qual := readOut(t, outDir, "qualified_live.m3u")
	if !strings.Contains(qual, "A") || !strings.Contains(qual, "C") {
		t.Fatalf("qualified_live.m3u missing A/C:\n%s", qual)
	}
	if strings.Contains(qual, "B") {
		t.Fatalf("low-quality B leaked into qualified_live.m3u:\n%s", qual)
	}
	_ = files
}

// TestGenerateFromTest_OutputAllValid verifies the output_all_valid switch: when
// on, the live.m3u base file bypasses resolution grouping and uses the full
// valid set, while qualified still applies quality filtering.
func TestGenerateFromTest_OutputAllValid(t *testing.T) {
	conn, err := db.Open(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	defer conn.Close()

	cfg := config.New(conn)
	outDir := t.TempDir()
	cfg.Set("Output", "output_dir", outDir)
	cfg.Set("Output", "enable_filter", "True")
	cfg.Set("Output", "output_all_valid", "True")

	mgr := New(conn, cfg)

	a := types.Channel{ID: "a", Name: "A", URL: "http://192.168.1.1/a.m3u8"}
	b := types.Channel{ID: "b", Name: "B", URL: "http://192.168.1.2/b.m3u8"}
	sess := &RealtimeSession{
		ID:       "s",
		channels: []types.Channel{a, b},
		Progress: types.TestProgress{Status: "done"},
		Results: map[string]types.TestResult{
			"a": {ID: "a", Status: "success", ResponseTime: 0.3, DownloadSpeed: 1200, Bitrate: 2000, Resolution: "1920x1080", HasVideoStream: true},
			"b": {ID: "b", Status: "success", ResponseTime: 1.0, DownloadSpeed: 10, Bitrate: 1000, Resolution: "1280x720", HasVideoStream: true},
		},
	}
	mgr.sessions["s"] = sess

	if _, _, gerr := mgr.GenerateFromTest("s"); gerr != nil {
		t.Fatalf("GenerateFromTest error: %v", gerr)
	}

	// live.m3u = full valid set (both A and B) despite filtering on
	base := readOut(t, outDir, "live.m3u")
	if !strings.Contains(base, "A") || !strings.Contains(base, "B") {
		t.Fatalf("output_all_valid: live.m3u should contain both A and B:\n%s", base)
	}
	// qualified still quality-filtered (B dropped)
	qual := readOut(t, outDir, "qualified_live.m3u")
	if strings.Contains(qual, "B") {
		t.Fatalf("output_all_valid must not bypass quality filter for qualified:\n%s", qual)
	}
}

// TestGenerateFromTest_EdgeCases verifies the refusal paths:
//   - unknown session -> error
//   - session not finished (Status != "done") -> error
//   - no success channel -> error, nothing written
func TestGenerateFromTest_EdgeCases(t *testing.T) {
	conn, err := db.Open(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	defer conn.Close()
	cfg := config.New(conn)
	cfg.Set("Output", "output_dir", t.TempDir())
	mgr := New(conn, cfg)

	// unknown session
	if _, _, e := mgr.GenerateFromTest("nope"); e == nil {
		t.Fatal("expected error for unknown session")
	}

	// not finished
	mgr.sessions["running"] = &RealtimeSession{
		ID:       "running",
		channels: []types.Channel{{ID: "x", Name: "X", URL: "u"}},
		Progress: types.TestProgress{Status: "running"},
		Results:  map[string]types.TestResult{},
	}
	if _, _, e := mgr.GenerateFromTest("running"); e == nil {
		t.Fatal("expected error for not-finished session")
	}

	// all failed -> no output
	mgr.sessions["allfail"] = &RealtimeSession{
		ID:       "allfail",
		channels: []types.Channel{{ID: "f1", Name: "F", URL: "uf"}},
		Progress: types.TestProgress{Status: "done"},
		Results:  map[string]types.TestResult{"f1": {ID: "f1", Status: "failed"}},
	}
	if _, _, e := mgr.GenerateFromTest("allfail"); e == nil {
		t.Fatal("expected error when no success channel")
	}
}
