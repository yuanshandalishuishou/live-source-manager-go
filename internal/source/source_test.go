package source

import "testing"

const sampleM3U = `#EXTM3U
#EXTINF:-1 tvg-id="cctv1" tvg-name="CCTV1" tvg-logo="http://e.com/1.png" group-title="央视",CCTV-1
http://example.com/cctv1.m3u8
#EXTINF:-1,Local Channel
file:///etc/passwd
#EXTINF:-1,RTMP Source
rtmp://example.com/live/2
`

func TestParseM3U(t *testing.T) {
	chs := ParseM3U(sampleM3U, "f1", "test.m3u")
	// file:// is excluded by the narrow parse-stage gate -> 2 channels remain
	if len(chs) != 2 {
		t.Fatalf("expected 2 channels, got %d", len(chs))
	}
	for _, c := range chs {
		if c.FileID != "f1" || c.FileName != "test.m3u" {
			t.Errorf("stamp mismatch: %+v", c)
		}
	}
	// First channel should carry the EXTINF metadata (name from tvg-name).
	if chs[0].Name != "CCTV1" {
		t.Errorf("expected name CCTV1, got %q", chs[0].Name)
	}
	if chs[0].Group != "央视" {
		t.Errorf("expected group 央视, got %q", chs[0].Group)
	}
	if chs[0].Logo != "http://e.com/1.png" {
		t.Errorf("expected logo, got %q", chs[0].Logo)
	}
}

func TestParseM3UExclusions(t *testing.T) {
	exclusions := map[string]string{}
	parseM3U(sampleM3U, "f1", "test.m3u", exclusions)
	if _, ok := exclusions["file:///etc/passwd"]; !ok {
		t.Fatal("unsafe URL should be recorded in exclusions map")
	}
}

func TestParseTXT(t *testing.T) {
	txt := `http://example.com/a.m3u8
rtmp://example.com/b
file:///secret
javascript:alert(1)
`
	chs := ParseTXT(txt, "f2", "list.txt")
	if len(chs) != 2 {
		t.Fatalf("expected 2 channels, got %d", len(chs))
	}
	for _, c := range chs {
		if c.URL == "file:///secret" || c.URL == "javascript:alert(1)" {
			t.Errorf("unsafe URL leaked: %q", c.URL)
		}
	}
}

// TestParseTXTSkipsComments verifies that when M3U content is mistakenly fed to
// ParseTXT (e.g. due to a .txt extension), playlist directive/comment lines are
// skipped instead of being logged as bogus URLs — fixing the 3362-line log spam.
func TestParseTXTSkipsComments(t *testing.T) {
	txt := `#EXTM3U
#EXTINF:-1 tvg-name="CCTV1",CCTV-1
http://example.com/cctv1.m3u8
#http://commented.out/x.m3u8
#EXTINF:-1,RTMP Source
rtmp://example.com/live/2
`
	chs := ParseTXT(txt, "f9", "mislabeled.txt")
	if len(chs) != 2 {
		t.Fatalf("expected 2 channels, got %d: %+v", len(chs), chs)
	}
	if chs[0].URL != "http://example.com/cctv1.m3u8" {
		t.Errorf("unexpected first URL: %q", chs[0].URL)
	}
	if chs[1].URL != "rtmp://example.com/live/2" {
		t.Errorf("unexpected second URL: %q", chs[1].URL)
	}
}

func TestLooksLikeHTML(t *testing.T) {
	cases := []struct {
		name string
		in   string
		want bool
	}{
		{"doctype", "<!DOCTYPE html><html><body>x</body></html>", true},
		{"meta", "<meta property=\"og:title\" content=\"x\">", true},
		{"m3u", "#EXTM3U\nhttp://example.com/a.m3u8", false},
		{"plain-url", "http://example.com/a.m3u8", false},
		{"empty", "", false},
	}
	for _, c := range cases {
		if got := looksLikeHTML(c.in); got != c.want {
			t.Errorf("%s: looksLikeHTML=%v want %v", c.name, got, c.want)
		}
	}
}

func TestLooksLikeM3U(t *testing.T) {
	cases := []struct {
		name string
		in   string
		want bool
	}{
		{"extm3u", "#EXTM3U\nhttp://example.com/a.m3u8", true},
		{"extinf", "#EXTINF:-1,CCTV1\nhttp://example.com/a.m3u8", true},
		{"comment-then-extinf", "# My List\n#EXTINF:-1,CCTV1\nhttp://x", true},
		{"plain-url", "http://example.com/a.m3u8\nrtmp://x/b", false},
		{"empty", "", false},
	}
	for _, c := range cases {
		if got := looksLikeM3U(c.in); got != c.want {
			t.Errorf("%s: looksLikeM3U=%v want %v", c.name, got, c.want)
		}
	}
}
