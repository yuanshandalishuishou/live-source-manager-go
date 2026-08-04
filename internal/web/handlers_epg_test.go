package web

import (
	"bytes"
	"context"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"

	"live-source-manager-go/internal/config"
	"live-source-manager-go/internal/db"
	"live-source-manager-go/internal/epg"
	"live-source-manager-go/internal/manager"
	"live-source-manager-go/internal/types"
)

// newEPGTestServer 构建一个最小可用的 web.Server（仅用于直接调用 handler 做单测，
// 不经过 ServeHTTP，因此不触发 CSRF 校验；admin 鉴权通过 context 注入）。
func newEPGTestServer(t *testing.T) *Server {
	t.Helper()
	dbPath := filepath.Join(t.TempDir(), "app.db")
	conn, err := db.Open(dbPath)
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	t.Cleanup(func() { conn.Close() })
	cfg := config.New(conn)
	if _, err := db.SeedDefaults(conn, config.DefaultValues()); err != nil {
		t.Fatalf("seed defaults: %v", err)
	}
	// 测试隔离：把发布目录指向临时目录，避免写入仓库内相对路径 ./www/output 造成文件锁冲突。
	cfg.Set("HTTPServer", "document_root", t.TempDir())
	mgr := manager.New(conn, cfg)
	em := epg.New(conn, cfg)
	return &Server{conn: conn, cfg: cfg, mgr: mgr, epgMgr: em}
}

// adminReq 构造一个带 admin 身份（可选路由参数）的请求并调用 handler。
func adminReq(s *Server, method, path string, body []byte, params map[string]string, admin bool, h func(http.ResponseWriter, *http.Request)) *httptest.ResponseRecorder {
	r := httptest.NewRequest(method, path, bytes.NewReader(body))
	if admin {
		r = r.WithContext(context.WithValue(r.Context(), ctxUserKey, &types.User{Role: "admin", Username: "admin"}))
	}
	if params != nil {
		r = r.WithContext(context.WithValue(r.Context(), ctxParamKey, params))
	}
	w := httptest.NewRecorder()
	h(w, r)
	return w
}

func TestEPGHandlers(t *testing.T) {
	s := newEPGTestServer(t)

	// 1) 预置源已种子：列表应含 7 条
	w := adminReq(s, "GET", "/api/epg/sources", nil, nil, true, s.hListEPGSources)
	if w.Code != http.StatusOK {
		t.Fatalf("list sources code=%d body=%s", w.Code, w.Body.String())
	}
	if !bytes.Contains(w.Body.Bytes(), []byte(`"count":7`)) {
		t.Fatalf("expected 7 seeded sources, got: %s", w.Body.String())
	}

	// 2) 创建新源（admin）
	w = adminReq(s, "POST", "/api/epg/sources", []byte(`{"name":"单元测试源","url":"https://example.test/epg.xml.gz","enabled":true,"priority":55}`), nil, true, s.hCreateEPGSource)
	if w.Code != http.StatusOK {
		t.Fatalf("create source code=%d body=%s", w.Code, w.Body.String())
	}

	// 3) 非 admin 创建被拒
	w = adminReq(s, "POST", "/api/epg/sources", []byte(`{"url":"https://x/y.gz"}`), nil, false, s.hCreateEPGSource)
	if w.Code != http.StatusForbidden {
		t.Fatalf("non-admin create should be 403, got %d", w.Code)
	}

	// 4) 空 url 创建校验
	w = adminReq(s, "POST", "/api/epg/sources", []byte(`{"name":"无地址","url":""}`), nil, true, s.hCreateEPGSource)
	if w.Code != http.StatusBadRequest {
		t.Fatalf("empty url should be 400, got %d", w.Code)
	}

	// 5) 状态接口
	w = adminReq(s, "GET", "/api/epg/status", nil, nil, true, s.hEPGStatus)
	if w.Code != http.StatusOK || !bytes.Contains(w.Body.Bytes(), []byte(`"enabled":true`)) {
		t.Fatalf("status unexpected: %d %s", w.Code, w.Body.String())
	}

	// 6) 网格（无数据，应返回 ok + 空行）
	w = adminReq(s, "GET", "/api/epg/grid?day=0&limit=10", nil, nil, true, s.hEPGGrid)
	if w.Code != http.StatusOK || !bytes.Contains(w.Body.Bytes(), []byte(`"ok":true`)) {
		t.Fatalf("grid unexpected: %d %s", w.Code, w.Body.String())
	}

	// 7) 频道列表（空）
	w = adminReq(s, "GET", "/api/epg/channels?page=1&page_size=5", nil, nil, true, s.hListEPGChannels)
	if w.Code != http.StatusOK {
		t.Fatalf("channels code=%d", w.Code)
	}

	// 8) NowNext（空）
	w = adminReq(s, "GET", "/api/epg/now?limit=5", nil, nil, true, s.hEPGNowNext)
	if w.Code != http.StatusOK {
		t.Fatalf("now code=%d", w.Code)
	}

	// 9) 生成 XMLTV（应写出文件并返回 ok）
	w = adminReq(s, "POST", "/api/epg/generate", nil, nil, true, s.hGenerateEPG)
	if w.Code != http.StatusOK || !bytes.Contains(w.Body.Bytes(), []byte(`"ok":true`)) {
		t.Fatalf("generate unexpected: %d %s", w.Code, w.Body.String())
	}

	// 10) URL 接口
	w = adminReq(s, "GET", "/api/epg/url", nil, nil, true, s.hEPGURL)
	if w.Code != http.StatusOK || !bytes.Contains(w.Body.Bytes(), []byte(`"ok":true`)) {
		t.Fatalf("url unexpected: %d %s", w.Code, w.Body.String())
	}

	// 11) 更新第 8 号源（刚创建的）
	w = adminReq(s, "PUT", "/api/epg/sources/8", []byte(`{"enabled":false,"priority":77}`), map[string]string{"source_id": "8"}, true, s.hUpdateEPGSource)
	if w.Code != http.StatusOK {
		t.Fatalf("update source code=%d body=%s", w.Code, w.Body.String())
	}

	// 12) 不存在的源更新 -> 404
	w = adminReq(s, "PUT", "/api/epg/sources/99999", []byte(`{"enabled":false}`), map[string]string{"source_id": "99999"}, true, s.hUpdateEPGSource)
	if w.Code != http.StatusNotFound {
		t.Fatalf("update missing source should be 404, got %d", w.Code)
	}

	// 13) 频道对齐：先插一条频道，再 match
	ch := types.EPGChannel{TVGID: "cctv1.test", DisplayName: "CCTV-1", Icon: "http://i/c1.png"}
	if err := db.ReplaceEPGData(s.conn, 8, []types.EPGChannel{ch}, nil); err != nil {
		t.Fatalf("replace epg data: %v", err)
	}
	got, err := db.GetEPGChannel(s.conn, 1)
	if err != nil || got == nil {
		t.Fatalf("get epg channel: %v %v", got, err)
	}
	w = adminReq(s, "POST", "/api/epg/channels/1/match", []byte(`{"matched_channel":"CCTV-1 综合","tvg_id":"cctv1.test"}`), map[string]string{"channel_id": "1"}, true, s.hMatchEPGChannel)
	if w.Code != http.StatusOK {
		t.Fatalf("match code=%d body=%s", w.Code, w.Body.String())
	}
	// 校验回写到 channel_name_mapping
	tvg, err := db.GetAllChannelTVGInfo(s.conn)
	if err != nil {
		t.Fatalf("get tvg info: %v", err)
	}
	if v, ok := tvg["CCTV-1 综合"]; !ok || v[0] != "cctv1.test" {
		t.Fatalf("channel_name_mapping tvg not written back: %v", tvg)
	}

	// 14) 删除刚创建的源（连同频道）
	w = adminReq(s, "DELETE", "/api/epg/sources/8", nil, map[string]string{"source_id": "8"}, true, s.hDeleteEPGSource)
	if w.Code != http.StatusOK {
		t.Fatalf("delete source code=%d", w.Code)
	}
}
