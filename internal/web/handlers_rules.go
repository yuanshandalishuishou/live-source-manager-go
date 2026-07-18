package web

import (
	"database/sql"
	"encoding/json"
	"net/http"
	"os"
	"strconv"
	"strings"

	"gopkg.in/yaml.v3"
	"live-source-manager-go/internal/db"
	"live-source-manager-go/internal/types"
)

var validDimensions = []string{"content", "region", "language", "quality", "media_type", "genre"}
var dimensionLabels = map[string]string{
	"content":    "内容分类",
	"region":     "地域",
	"language":   "语言",
	"quality":    "画质",
	"media_type": "媒体类型",
	"genre":      "节目类型",
}
var defaultDimensions = [][2]string{
	{"content", "内容分类"}, {"region", "地域"}, {"language", "语言"},
	{"quality", "画质"}, {"media_type", "媒体类型"}, {"genre", "节目类型"},
}

type ymlRule struct {
	Name     string   `yaml:"name"`
	Priority int      `yaml:"priority"`
	Keywords []string `yaml:"keywords"`
}
type ymlFile struct {
	Categories   []ymlRule           `yaml:"categories"`
	ChannelTypes map[string][]string `yaml:"channel_types"`
}

func rulesYAMLPath() string {
	cands := []string{"config/channel_rules.yml", "channel_rules.yml"}
	for _, c := range cands {
		if _, err := os.Stat(c); err == nil {
			return c
		}
	}
	return "config/channel_rules.yml"
}

func seedRulesFromYAML(conn *sql.DB, path string) (int, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return 0, err
	}
	var f ymlFile
	if err := yaml.Unmarshal(data, &f); err != nil {
		return 0, err
	}
	if _, err := conn.Exec("DELETE FROM classification_rules"); err != nil {
		return 0, err
	}
	idx := 0
	for _, c := range f.Categories {
		if c.Name == "" {
			continue
		}
		if _, err := db.CreateRule(conn, "content", c.Name, c.Keywords, c.Priority, idx, true); err != nil {
			return idx, err
		}
		idx++
	}
	for k, v := range f.ChannelTypes {
		if _, err := db.CreateRule(conn, "media_type", k, v, 50, idx, true); err != nil {
			return idx, err
		}
		idx++
	}
	return idx, nil
}

func (s *Server) hListRules(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	ruleType := q.Get("rule_type")
	activeOnly := q.Get("active_only") == "1"
	var rules []types.ClassificationRule
	var err error
	if activeOnly {
		rules, err = db.GetActiveRules(s.conn)
	} else {
		rules, err = db.ListRules(s.conn)
	}
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]any{"ok": false, "error": err.Error()})
		return
	}
	if ruleType != "" {
		var filtered []types.ClassificationRule
		for _, ru := range rules {
			if ru.RuleType == ruleType {
				filtered = append(filtered, ru)
			}
		}
		rules = filtered
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "rules": rules})
}

func (s *Server) hCreateRule(w http.ResponseWriter, r *http.Request) {
	m := decodeBody(r)
	ruleType := strField(m, "rule_type")
	name := strField(m, "name")
	if ruleType == "" || name == "" {
		writeJSON(w, http.StatusBadRequest, map[string]any{"ok": false, "error": "rule_type 和 name 不能为空"})
		return
	}
	keywords := toStringSlice(m["keywords"])
	priority := atoiDefault(m["priority"], 100)
	sortOrder := atoiDefault(m["sort_order"], 0)
	id, err := db.CreateRule(s.conn, ruleType, name, keywords, priority, sortOrder, true)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]any{"ok": false, "error": err.Error()})
		return
	}
	s.audit(r, "rule_create", "["+ruleType+"] "+name, "")
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "id": id, "message": "规则已创建"})
}

func (s *Server) hUpdateRule(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.Atoi(routeParam(r, "rule_id"))
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]any{"ok": false, "error": "无效的规则 ID"})
		return
	}
	m := decodeBody(r)
	fields := map[string]any{}
	if v, ok := m["name"]; ok {
		fields["name"] = strField(m, "name")
		_ = v
	}
	if m["keywords"] != nil {
		fields["keywords"] = toStringSlice(m["keywords"])
	}
	if _, ok := m["priority"]; ok {
		fields["priority"] = atoiDefault(m["priority"], 100)
	}
	if _, ok := m["sort_order"]; ok {
		fields["sort_order"] = atoiDefault(m["sort_order"], 0)
	}
	if _, ok := m["is_active"]; ok {
		fields["is_active"] = atoiDefault(m["is_active"], 1) != 0
	}
	if v, ok := m["rule_type"]; ok {
		fields["rule_type"] = strField(m, "rule_type")
		_ = v
	}
	if len(fields) == 0 {
		writeJSON(w, http.StatusBadRequest, map[string]any{"ok": false, "error": "没有需要更新的字段"})
		return
	}
	if err := updateRulePartial(s.conn, id, fields); err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]any{"ok": false, "error": err.Error()})
		return
	}
	s.audit(r, "rule_update", strconv.Itoa(id), "更新规则字段")
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "message": "规则已更新"})
}

func updateRulePartial(conn *sql.DB, id int, fields map[string]any) error {
	if name, ok := fields["name"].(string); ok {
		if _, err := conn.Exec("UPDATE classification_rules SET name=? WHERE id=?", name, id); err != nil {
			return err
		}
	}
	if rt, ok := fields["rule_type"].(string); ok {
		if _, err := conn.Exec("UPDATE classification_rules SET rule_type=? WHERE id=?", rt, id); err != nil {
			return err
		}
	}
	if kw, ok := fields["keywords"].([]string); ok {
		kb, _ := json.Marshal(kw)
		if _, err := conn.Exec("UPDATE classification_rules SET keywords=? WHERE id=?", string(kb), id); err != nil {
			return err
		}
	}
	if p, ok := fields["priority"].(int); ok {
		if _, err := conn.Exec("UPDATE classification_rules SET priority=? WHERE id=?", p, id); err != nil {
			return err
		}
	}
	if so, ok := fields["sort_order"].(int); ok {
		if _, err := conn.Exec("UPDATE classification_rules SET sort_order=? WHERE id=?", so, id); err != nil {
			return err
		}
	}
	if ia, ok := fields["is_active"].(bool); ok {
		v := 0
		if ia {
			v = 1
		}
		if _, err := conn.Exec("UPDATE classification_rules SET is_active=? WHERE id=?", v, id); err != nil {
			return err
		}
	}
	return nil
}

func (s *Server) hDeleteRule(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.Atoi(routeParam(r, "rule_id"))
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]any{"ok": false, "error": "无效的规则 ID"})
		return
	}
	if err := db.DeleteRule(s.conn, id); err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]any{"ok": false, "error": err.Error()})
		return
	}
	s.audit(r, "rule_delete", strconv.Itoa(id), "")
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "message": "规则已删除"})
}

func (s *Server) hBatchOrderRules(w http.ResponseWriter, r *http.Request) {
	m := decodeBody(r)
	orders, ok := m["orders"].([]any)
	if !ok {
		writeJSON(w, http.StatusBadRequest, map[string]any{"ok": false, "error": "orders 不能为空"})
		return
	}
	var ids []int
	for _, o := range orders {
		om, ok := o.(map[string]any)
		if !ok {
			continue
		}
		if idf, ok := om["id"].(float64); ok {
			ids = append(ids, int(idf))
		}
	}
	if len(ids) == 0 {
		writeJSON(w, http.StatusBadRequest, map[string]any{"ok": false, "error": "orders 不能为空"})
		return
	}
	if err := db.BatchOrder(s.conn, ids); err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]any{"ok": false, "error": err.Error()})
		return
	}
	s.audit(r, "rule_batch_order", "", "批量更新规则排序")
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "message": "排序已更新"})
}

func (s *Server) hListDimensions(w http.ResponseWriter, r *http.Request) {
	dims, err := db.ListDimensions(s.conn)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]any{"ok": false, "error": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "dimensions": dims})
}

func (s *Server) hCreateDimension(w http.ResponseWriter, r *http.Request) {
	m := decodeBody(r)
	dimKey := strField(m, "dim_key")
	dimName := strField(m, "dim_name")
	if dimKey == "" || dimName == "" {
		writeJSON(w, http.StatusBadRequest, map[string]any{"ok": false, "error": "dim_key 和 dim_name 不能为空"})
		return
	}
	if err := db.CreateDimension(s.conn, dimKey, dimName, atoiDefault(m["sort_order"], 0)); err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]any{"ok": false, "error": err.Error()})
		return
	}
	s.audit(r, "dimension_create", dimKey, "创建维度 "+dimName)
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "message": "维度已创建"})
}

func (s *Server) hDeleteDimension(w http.ResponseWriter, r *http.Request) {
	dimKey := routeParam(r, "dim_key")
	if err := db.DeleteDimension(s.conn, dimKey); err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]any{"ok": false, "error": err.Error()})
		return
	}
	s.audit(r, "dimension_delete", dimKey, "")
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "message": "维度已删除"})
}

func (s *Server) hListExclusions(w http.ResponseWriter, r *http.Request) {
	ex, err := db.ListExclusions(s.conn)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]any{"ok": false, "error": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "exclusions": ex})
}

func (s *Server) hCreateExclusion(w http.ResponseWriter, r *http.Request) {
	m := decodeBody(r)
	prov := strField(m, "province_keyword")
	excl := strField(m, "excluded_keyword")
	if prov == "" || excl == "" {
		writeJSON(w, http.StatusBadRequest, map[string]any{"ok": false, "error": "province_keyword 和 excluded_keyword 不能为空"})
		return
	}
	id, err := db.CreateExclusion(s.conn, prov, excl, strField(m, "note"))
	if err != nil {
		writeJSON(w, http.StatusConflict, map[string]any{"ok": false, "error": "排除映射已存在"})
		return
	}
	s.audit(r, "exclusion_create", prov+"->"+excl, "")
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "id": id, "message": "排除映射已添加"})
}

func (s *Server) hDeleteExclusion(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.Atoi(routeParam(r, "exclusion_id"))
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]any{"ok": false, "error": "无效的排除 ID"})
		return
	}
	if err := db.DeleteExclusion(s.conn, id); err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]any{"ok": false, "error": err.Error()})
		return
	}
	s.audit(r, "exclusion_delete", strconv.Itoa(id), "")
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "message": "排除映射已删除"})
}

func (s *Server) hReimportRules(w http.ResponseWriter, r *http.Request) {
	path := rulesYAMLPath()
	count, err := seedRulesFromYAML(s.conn, path)
	if err != nil {
		writeJSON(w, http.StatusNotFound, map[string]any{"ok": false, "error": "无法导入规则文件：" + err.Error()})
		return
	}
	s.audit(r, "rules_reimport", "channel_rules.yml", "重新导入 "+strconv.Itoa(count)+" 条规则")
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "count": count, "message": "已从 YAML 重新导入规则"})
}

func (s *Server) hResetRules(w http.ResponseWriter, r *http.Request) {
	path := rulesYAMLPath()
	count := 0
	if _, err := os.Stat(path); err == nil {
		c, e := seedRulesFromYAML(s.conn, path)
		if e == nil {
			count = c
		}
	} else {
		_, _ = s.conn.Exec("DELETE FROM classification_rules")
		_, _ = s.conn.Exec("DELETE FROM classification_dimensions")
		for i, d := range defaultDimensions {
			_ = db.CreateDimension(s.conn, d[0], d[1], i)
		}
	}
	s.audit(r, "rules_reset_defaults", "classification_rules", "恢复默认分类规则")
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "count": count, "message": "已恢复默认分类规则"})
}

func (s *Server) hTestClassification(w http.ResponseWriter, r *http.Request) {
	m := decodeBody(r)
	name := strField(m, "channel_name")
	if name == "" {
		writeJSON(w, http.StatusBadRequest, map[string]any{"ok": false, "error": "channel_name 不能为空"})
		return
	}
	channels := []types.Channel{{Name: name}}
	if err := s.eng.LoadRules(); err != nil {
		loggerWarn("加载规则失败: %v", err)
	}
	_ = s.eng.Classify(channels)
	cats := channels[0].Categories
	matches := map[string][]map[string]any{}
	rules, _ := db.ListRules(s.conn)
	upper := strings.ToUpper(name)
	for _, ru := range rules {
		if !ru.IsActive {
			continue
		}
		for _, kw := range ru.Keywords {
			if kw != "" && strings.Contains(upper, strings.ToUpper(kw)) {
				matches[ru.RuleType] = append(matches[ru.RuleType], map[string]any{
					"keyword":   kw,
					"rule_name": ru.Name,
					"priority":  ru.Priority,
				})
				break
			}
		}
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "categories": cats, "matches_by_dim": matches})
}

// ── channel mappings ───────────────────────────────────────────────

func (s *Server) hListChannelMappings(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	search := q.Get("q")
	limit, _ := strconv.Atoi(q.Get("limit"))
	offset, _ := strconv.Atoi(q.Get("offset"))
	if limit <= 0 {
		limit = 50
	}
	maps, err := db.ListChannelMappings(s.conn, limit, offset, search)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]any{"ok": false, "error": err.Error()})
		return
	}
	total := s.countChannelMappings(search)
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "mappings": maps, "total": total, "limit": limit, "offset": offset})
}

func (s *Server) countChannelMappings(search string) int {
	var cnt int
	qq := "SELECT COUNT(*) FROM channel_name_mapping"
	args := []any{}
	if search != "" {
		qq += " WHERE channel_name LIKE ?"
		args = append(args, "%"+search+"%")
	}
	_ = s.conn.QueryRow(qq, args...).Scan(&cnt)
	return cnt
}

func (s *Server) hGetChannelMapping(w http.ResponseWriter, r *http.Request) {
	name := routeParam(r, "channel_name")
	m, err := db.GetChannelMapping(s.conn, name)
	if err != nil || m == nil {
		writeJSON(w, http.StatusNotFound, map[string]any{"ok": false, "error": "未找到该频道的全名映射"})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "mapping": m})
}

func (s *Server) hSaveChannelMapping(w http.ResponseWriter, r *http.Request) {
	name := routeParam(r, "channel_name")
	m := decodeBody(r)
	cats := map[string]string{}
	for _, d := range validDimensions {
		if v := strField(m, d); v != "" {
			cats[d] = v
		}
	}
	if len(cats) == 0 {
		writeJSON(w, http.StatusBadRequest, map[string]any{"ok": false, "error": "至少需要提供一个维度的分类值"})
		return
	}
	if err := db.SaveChannelMapping(s.conn, name, cats); err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]any{"ok": false, "error": err.Error()})
		return
	}
	s.audit(r, "channel_mapping_save", name, "保存频道映射")
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "message": name + " 映射已保存", "categories": cats})
}

func (s *Server) hDeleteChannelMapping(w http.ResponseWriter, r *http.Request) {
	name := routeParam(r, "channel_name")
	if err := db.DeleteChannelMapping(s.conn, name); err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]any{"ok": false, "error": err.Error()})
		return
	}
	s.audit(r, "channel_mapping_delete", name, "")
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "message": name + " 映射已删除"})
}

func (s *Server) hBatchImportMappings(w http.ResponseWriter, r *http.Request) {
	channels, _ := s.collectChannels()
	count := 0
	for _, ch := range channels {
		if ch.Name == "" {
			continue
		}
		cats := map[string]string{}
		if ch.Categories != nil {
			for _, d := range validDimensions {
				if v, ok := ch.Categories[d]; ok && v != "" {
					cats[d] = v
				}
			}
		}
		if len(cats) == 0 {
			continue
		}
		if err := db.SaveChannelMapping(s.conn, ch.Name, cats); err == nil {
			count++
		}
	}
	s.audit(r, "channel_mapping_batch_import", "", "批量导入 "+strconv.Itoa(count)+" 条映射")
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "message": "批量导入完成，共 " + strconv.Itoa(count) + " 条", "count": count})
}

// ── category dictionary ──────────────────────────────────────────

func (s *Server) hGetCategoryDictionary(w http.ResponseWriter, r *http.Request) {
	raw, err := db.GetCategoryDictionary(s.conn)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]any{"ok": false, "error": err.Error()})
		return
	}
	var dims []map[string]any
	for _, d := range validDimensions {
		opts := raw[d]
		if opts == nil {
			opts = []types.CategoryDictValue{}
		}
		dims = append(dims, map[string]any{
			"key":     d,
			"label":   dimensionLabels[d],
			"options": opts,
		})
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "dimensions": dims, "raw": raw})
}

func (s *Server) hResetCategoryDictionary(w http.ResponseWriter, r *http.Request) {
	path := rulesYAMLPath()
	if _, err := os.Stat(path); err == nil {
		if err := s.eng.SeedDictionary(path); err != nil {
			loggerWarn("重新种子分类字典失败: %v", err)
		}
	} else {
		_, _ = s.conn.Exec("DELETE FROM category_dictionary")
	}
	s.audit(r, "category_dict_reset_defaults", "category_dictionary", "")
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "message": "已恢复默认分类字典"})
}

func (s *Server) hAddCategoryValue(w http.ResponseWriter, r *http.Request) {
	dim := routeParam(r, "dimension")
	if !isValidDimension(dim) {
		writeJSON(w, http.StatusBadRequest, map[string]any{"ok": false, "error": "无效的维度: " + dim})
		return
	}
	m := decodeBody(r)
	value := strField(m, "value")
	label := strField(m, "label")
	if label == "" {
		label = value
	}
	if value == "" {
		writeJSON(w, http.StatusBadRequest, map[string]any{"ok": false, "error": "value 不能为空"})
		return
	}
	if err := db.AddCategoryValue(s.conn, dim, value, label, atoiDefault(m["sort_order"], 99)); err != nil {
		writeJSON(w, http.StatusConflict, map[string]any{"ok": false, "error": "选项已存在或添加失败"})
		return
	}
	s.audit(r, "category_dict_add", dim, "添加选项 "+value)
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "message": dimensionLabels[dim] + " 选项 " + value + " 已添加"})
}

func (s *Server) hSetCategoryDimension(w http.ResponseWriter, r *http.Request) {
	dim := routeParam(r, "dimension")
	if !isValidDimension(dim) {
		writeJSON(w, http.StatusBadRequest, map[string]any{"ok": false, "error": "无效的维度: " + dim})
		return
	}
	m := decodeBody(r)
	opts, ok := m["options"].([]any)
	if !ok {
		writeJSON(w, http.StatusBadRequest, map[string]any{"ok": false, "error": "options 必须是数组"})
		return
	}
	if _, err := s.conn.Exec("DELETE FROM category_dictionary WHERE dimension=?", dim); err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]any{"ok": false, "error": err.Error()})
		return
	}
	for i, o := range opts {
		val := ""
		switch v := o.(type) {
		case string:
			val = v
		case map[string]any:
			val = strField(v, "value")
		}
		if val == "" {
			continue
		}
		_ = db.AddCategoryValue(s.conn, dim, val, val, i)
	}
	s.audit(r, "category_dict_set", dim, "设置维度选项，共 "+strconv.Itoa(len(opts))+" 项")
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "message": dimensionLabels[dim] + " 已更新，共 " + strconv.Itoa(len(opts)) + " 项"})
}

func (s *Server) hDeleteCategoryValue(w http.ResponseWriter, r *http.Request) {
	dim := routeParam(r, "dimension")
	value := routeParam(r, "value")
	if !isValidDimension(dim) {
		writeJSON(w, http.StatusBadRequest, map[string]any{"ok": false, "error": "无效的维度: " + dim})
		return
	}
	if err := db.DeleteCategoryValue(s.conn, dim, value); err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]any{"ok": false, "error": err.Error()})
		return
	}
	s.audit(r, "category_dict_delete", dim, "删除选项 "+value)
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "message": "选项 " + value + " 已删除"})
}

// ── shared helpers ──────────────────────────────────────────

func toStringSlice(v any) []string {
	switch t := v.(type) {
	case []string:
		return t
	case []any:
		out := make([]string, 0, len(t))
		for _, e := range t {
			if s, ok := e.(string); ok {
				out = append(out, s)
			}
		}
		return out
	case string:
		t = strings.TrimSpace(t)
		if t == "" {
			return nil
		}
		return []string{t}
	}
	return nil
}

func atoiDefault(v any, def int) int {
	switch t := v.(type) {
	case int:
		return t
	case float64:
		return int(t)
	case string:
		if n, err := strconv.Atoi(strings.TrimSpace(t)); err == nil {
			return n
		}
	}
	return def
}

func isValidDimension(d string) bool {
	for _, vd := range validDimensions {
		if vd == d {
			return true
		}
	}
	return false
}
