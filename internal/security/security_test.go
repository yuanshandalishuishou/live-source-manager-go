package security

import "testing"

func TestIsStaticSafe(t *testing.T) {
	cases := []struct {
		url  string
		safe bool
		cat  string
	}{
		{"http://example.com/live.m3u8", true, "ok"},
		{"https://example.com/stream", true, "ok"},
		{"https://1.2.3.4:554/stream", true, "ok"}, // public IP is allowed at parse stage
		{"rtmp://example.com/live/1", true, "ok"},
		{"rtsp://example.com/cam", true, "ok"},
		{"rtp://example.com/flow", true, "ok"},
		{"", false, "host"},
		{"   ", false, "host"},
		{"file:///etc/passwd", false, "scheme"},
		{"javascript:alert(1)", false, "scheme"},
		{"ftp://example.com/x", false, "scheme"},
		{"http://localhost:8080/x", false, "host"},
		{"http://127.0.0.1/x", false, "ssrf"},
		{"http://192.168.1.1/x", false, "ssrf"},
		{"http://10.0.0.5/x", false, "ssrf"},
		{"http://myhost.local/x", false, "ssrf"},
	}
	for _, c := range cases {
		ok, reason, cat := IsStaticSafe(c.url)
		if ok != c.safe {
			t.Errorf("IsStaticSafe(%q) safe=%v want %v (reason=%q)", c.url, ok, c.safe, reason)
		}
		if cat != c.cat {
			t.Errorf("IsStaticSafe(%q) cat=%q want %q", c.url, cat, c.cat)
		}
	}
}

func TestIsStaticSafeStripInline(t *testing.T) {
	// The parser strips "|User-Agent=..." suffixes; the gate should see the clean URL.
	ok, _, _ := IsStaticSafe("http://example.com/x.m3u8|User-Agent=Test")
	if !ok {
		t.Fatal("URL with inline UA suffix should be considered safe")
	}
}
