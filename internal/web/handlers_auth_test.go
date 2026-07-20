package web

import "testing"

// TestBoolField 覆盖 boolField 对 JSON 原生布尔与字符串两种传参的解析，
// 并验证「未提供」(reported=false) 与「显式 false」(reported=true) 的可区分性。
// 这是修复「文件级 UA 开关因 strField 无法解析布尔而永远写不进库」的回归锚点。
func TestBoolField(t *testing.T) {
	cases := []struct {
		in           any
		wantVal      bool
		wantReported bool
	}{
		{true, true, true},
		{false, false, true},
		{"true", true, true},
		{"false", false, true},
		{"1", true, true},
		{"0", false, true},
		{"on", true, true},
		{"off", false, true},
		{"yes", true, true},
		{"no", false, true},
		{"", false, true}, // 表单空值视为显式 false
		{nil, false, false},
	}
	for i, c := range cases {
		m := map[string]any{"k": c.in}
		v, rep := boolField(m, "k")
		if v != c.wantVal || rep != c.wantReported {
			t.Errorf("case %d in=%#v: got (%v,%v) want (%v,%v)", i, c.in, v, rep, c.wantVal, c.wantReported)
		}
	}
	if _, rep := boolField(map[string]any{}, "missing"); rep {
		t.Errorf("missing key should report false")
	}
}
