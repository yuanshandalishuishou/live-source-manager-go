package m3u

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"live-source-manager-go/internal/types"
)

func TestIsIPv6URL(t *testing.T) {
	cases := []struct {
		url  string
		want bool
	}{
		{"http://[2001:db8::1]/stream.m3u8", true},
		{"http://fe80::1:8080/x", true},
		{"http://example.com/stream.m3u8", false},
		{"http://192.168.1.1:8080/x", false},
		{"", false},
		{"not a url", false},
	}
	for _, c := range cases {
		if got := isIPv6URL(c.url); got != c.want {
			t.Errorf("isIPv6URL(%q) = %v, want %v", c.url, got, c.want)
		}
	}
}

func TestResolutionBasedGrouping(t *testing.T) {
	chs := []types.Channel{
		{ID: "a1", Name: "CCTV1", URL: "u1", Resolution: "1920x1080", DownloadSpeed: 500, ResponseTime: 1, Bitrate: 1000},
		{ID: "a2", Name: "CCTV1", URL: "u2", Resolution: "1920x1080", DownloadSpeed: 1500, ResponseTime: 0.5, Bitrate: 2000},
		{ID: "a3", Name: "CCTV1", URL: "u3", Resolution: "1920x1080", DownloadSpeed: 800, ResponseTime: 0.8, Bitrate: 1500},
		{ID: "b1", Name: "CCTV1", URL: "u4", Resolution: "1280x720", DownloadSpeed: 900, ResponseTime: 0.6, Bitrate: 1200},
		{ID: "r1", Name: "Radio1", URL: "u5", MediaType: "radio", DownloadSpeed: 700, ResponseTime: 0.4, Bitrate: 128},
	}
	// keep top 2 per group: 1080p→2 (a2,a3), 720p→1 (b1), radio→1 (r1) = 4
	got := ResolutionBasedGrouping(chs, 2)
	if len(got) != 4 {
		t.Fatalf("expected 4 kept (2x1080p + 1x720p + 1 radio), got %d: %+v", len(got), got)
	}
	// 1080p group must keep the two fastest (a2 speed1500, a3 speed800), drop a1 speed500
	ids := map[string]bool{}
	for _, c := range got {
		ids[c.ID] = true
	}
	if !ids["a2"] || !ids["a3"] || ids["a1"] {
		t.Fatalf("1080p group wrong: kept %v (want a2,a3 not a1)", ids)
	}
	if !ids["b1"] || !ids["r1"] {
		t.Fatalf("720p/radio groups dropped: %v", ids)
	}
}

func TestFilterQualified(t *testing.T) {
	filter := map[string]any{
		"min_speed": 50, "max_latency": 4000, "min_bitrate": 80,
		"min_resolution": "360p", "max_resolution": "4k", "resolution_filter_mode": "range",
		"must_hd": false, "must_4k": false,
	}
	chs := []types.Channel{
		{ID: "ok", Name: "OK", URL: "u", DownloadSpeed: 200, ResponseTime: 1, Bitrate: 1000, Resolution: "1920x1080"},
		{ID: "slow", Name: "SLOW", URL: "u", DownloadSpeed: 10, ResponseTime: 1, Bitrate: 1000, Resolution: "1920x1080"},
	}
	got := FilterQualified(chs, filter)
	if len(got) != 1 || got[0].ID != "ok" {
		t.Fatalf("FilterQualified = %+v, want [ok]", got)
	}
}

func TestWriteMultiFiles(t *testing.T) {
	dir := t.TempDir()
	opts := Options{
		Filename:           "live.m3u",
		OutputDir:          dir,
		SeparateIPv4IPv6:   true,
		IPv4Filename:       "live-ipv4.m3u",
		IPv6Filename:       "live-ipv6.m3u",
		GroupBy:            "category",
		SortBy:             "speed",
	}
	live := []types.Channel{
		{ID: "a", Name: "A", URL: "http://192.168.1.1/a.m3u8", Resolution: "1920x1080", DownloadSpeed: 1200, Categories: map[string]string{"content": "新闻"}},
		{ID: "c", Name: "C", URL: "http://[2001:db8::1]/c.m3u8", Resolution: "1920x1080", DownloadSpeed: 800, Categories: map[string]string{"content": "新闻"}},
	}
	qualified := []types.Channel{
		{ID: "a", Name: "A", URL: "http://192.168.1.1/a.m3u8", Resolution: "1920x1080", DownloadSpeed: 1200, Categories: map[string]string{"content": "新闻"}},
	}
	files, err := WriteMultiFiles(MultiSets{Live: live, Qualified: qualified}, opts)
	if err != nil {
		t.Fatalf("WriteMultiFiles error: %v", err)
	}
	want := map[string]bool{"live.m3u": true, "live-ipv4.m3u": true, "live-ipv6.m3u": true, "qualified_live.m3u": true}
	for _, f := range files {
		if !want[f] {
			t.Fatalf("unexpected file %q in %v", f, files)
		}
		delete(want, f)
	}
	if len(want) > 0 {
		t.Fatalf("missing files: %v (got %v)", want, files)
	}

	ipv4 := readFile(t, dir, "live-ipv4.m3u")
	if strings.Contains(ipv4, "C") || !strings.Contains(ipv4, "A") {
		t.Fatalf("live-ipv4.m3u wrong:\n%s", ipv4)
	}
	ipv6 := readFile(t, dir, "live-ipv6.m3u")
	if !strings.Contains(ipv6, "C") {
		t.Fatalf("live-ipv6.m3u missing C:\n%s", ipv6)
	}
}

func readFile(t *testing.T, dir, name string) string {
	t.Helper()
	b, err := os.ReadFile(filepath.Join(dir, name))
	if err != nil {
		t.Fatalf("read %s: %v", name, err)
	}
	return string(b)
}
