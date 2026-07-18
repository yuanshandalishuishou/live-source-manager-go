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
