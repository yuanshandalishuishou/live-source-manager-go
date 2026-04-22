package web

import (
	"database/sql"
	"fmt"
	"io"
	"net"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"

	"live-source-manager-go/internal/models"
	"live-source-manager-go/internal/source"

	"github.com/gin-gonic/gin"
)

// LoginPage 登录页面
func (s *Server) LoginPage(c *gin.Context) {
	c.HTML(http.StatusOK, "login.html", nil)
}

// Login 用户登录
func (s *Server) Login(c *gin.Context) {
	var req struct {
		Username string `json:"username"`
		Password string `json:"password"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	var user models.User
	err := s.db.QueryRow(`SELECT id, username, password_hash, is_admin FROM users WHERE username = ? AND is_active = 1`, req.Username).
		Scan(&user.ID, &user.Username, &user.PasswordHash, &user.IsAdmin)
	if err != nil {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "用户名或密码错误"})
		return
	}
	if !CheckPassword(user.PasswordHash, req.Password) {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "用户名或密码错误"})
		return
	}

	token, _ := GenerateToken(&user)
	c.SetCookie("token", token, 86400, "/", "", false, true)
	c.JSON(http.StatusOK, gin.H{"token": token, "username": user.Username, "is_admin": user.IsAdmin})
}

// IndexPage 首页
func (s *Server) IndexPage(c *gin.Context) {
	c.HTML(http.StatusOK, "index.html", gin.H{"username": c.GetString("username")})
}

// SourcesPage 源管理页面
func (s *Server) SourcesPage(c *gin.Context) {
	c.HTML(http.StatusOK, "sources.html", nil)
}

// SubscriptionsPage 订阅管理页面
func (s *Server) SubscriptionsPage(c *gin.Context) {
	c.HTML(http.StatusOK, "subscriptions.html", nil)
}

// CategoriesPage 分类管理页面
func (s *Server) CategoriesPage(c *gin.Context) {
	c.HTML(http.StatusOK, "categories.html", nil)
}

// DisplayRulesPage 显示规则页面
func (s *Server) DisplayRulesPage(c *gin.Context) {
	c.HTML(http.StatusOK, "display_rules.html", nil)
}

// ConfigPage 配置页面
func (s *Server) ConfigPage(c *gin.Context) {
	c.HTML(http.StatusOK, "config.html", nil)
}

// LogsPage 日志页面
func (s *Server) LogsPage(c *gin.Context) {
	c.HTML(http.StatusOK, "logs.html", nil)
}

// PreviewPage 预览页面
func (s *Server) PreviewPage(c *gin.Context) {
	c.HTML(http.StatusOK, "preview.html", nil)
}

// GetStats 获取系统统计信息
func (s *Server) GetStats(c *gin.Context) {
	var totalSources, activeSources int
	s.db.QueryRow(`SELECT COUNT(*) FROM url_sources_passed`).Scan(&totalSources)
	s.db.QueryRow(`SELECT COUNT(*) FROM url_sources_passed WHERE status = 'active'`).Scan(&activeSources)

	c.JSON(http.StatusOK, gin.H{
		"total_sources":  totalSources,
		"active_sources": activeSources,
		"last_update":    time.Now().Format(time.RFC3339),
	})
}

// ListSources 获取源列表（分页、过滤）
func (s *Server) ListSources(c *gin.Context) {
	page, _ := strconv.Atoi(c.DefaultQuery("page", "1"))
	pageSize, _ := strconv.Atoi(c.DefaultQuery("pageSize", "20"))
	status := c.Query("status")
	keyword := c.Query("keyword")

	offset := (page - 1) * pageSize
	query := `SELECT id, url, name, status, resolution, bitrate, response_time_ms, last_checked 
		FROM url_sources_passed WHERE 1=1`
	args := []interface{}{}
	if status != "" {
		query += " AND status = ?"
		args = append(args, status)
	}
	if keyword != "" {
		query += " AND (name LIKE ? OR url LIKE ?)"
		args = append(args, "%"+keyword+"%", "%"+keyword+"%")
	}
	query += " ORDER BY name LIMIT ? OFFSET ?"
	args = append(args, pageSize, offset)

	rows, err := s.db.Query(query, args...)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	defer rows.Close()

	var sources []map[string]interface{}
	for rows.Next() {
		var id int64
		var url, name, status, resolution sql.NullString
		var bitrate, responseTime sql.NullInt32
		var lastChecked sql.NullTime
		rows.Scan(&id, &url, &name, &status, &resolution, &bitrate, &responseTime, &lastChecked)
		sources = append(sources, map[string]interface{}{
			"id":            id,
			"url":           url.String,
			"name":          name.String,
			"status":        status.String,
			"resolution":    resolution.String,
			"bitrate":       bitrate.Int32,
			"response_time": responseTime.Int32,
			"last_checked":  lastChecked.Time,
		})
	}
	c.JSON(http.StatusOK, gin.H{"data": sources, "page": page, "pageSize": pageSize})
}

// ToggleSource 切换源启用状态
func (s *Server) ToggleSource(c *gin.Context) {
	id, _ := strconv.ParseInt(c.Param("id"), 10, 64)
	var req struct{ Enable bool }
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	status := "inactive"
	if req.Enable {
		status = "active"
	}
	_, err := s.db.Exec(`UPDATE url_sources_passed SET status = ? WHERE id = ?`, status, id)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, gin.H{"success": true})
}

// TestSingleSource 手动测试单个源
func (s *Server) TestSingleSource(c *gin.Context) {
	c.JSON(http.StatusOK, gin.H{"message": "测试任务已加入队列"})
}

// ListSubscriptions 获取订阅列表
func (s *Server) ListSubscriptions(c *gin.Context) {
	rows, err := s.db.Query(`SELECT id, name, url, update_interval, last_update, enable FROM subscriptions ORDER BY name`)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	defer rows.Close()
	var subs []models.Subscription
	for rows.Next() {
		var sub models.Subscription
		rows.Scan(&sub.ID, &sub.Name, &sub.URL, &sub.UpdateInterval, &sub.LastUpdate, &sub.Enable)
		subs = append(subs, sub)
	}
	c.JSON(http.StatusOK, gin.H{"data": subs})
}

// CreateSubscription 创建订阅
func (s *Server) CreateSubscription(c *gin.Context) {
	var sub models.Subscription
	if err := c.ShouldBindJSON(&sub); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	res, err := s.db.Exec(`INSERT INTO subscriptions (name, url, update_interval, enable) VALUES (?, ?, ?, ?)`,
		sub.Name, sub.URL, sub.UpdateInterval, sub.Enable)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	id, _ := res.LastInsertId()
	c.JSON(http.StatusOK, gin.H{"id": id})
}

// UpdateSubscription 更新订阅
func (s *Server) UpdateSubscription(c *gin.Context) {
	id, _ := strconv.ParseInt(c.Param("id"), 10, 64)
	var sub models.Subscription
	if err := c.ShouldBindJSON(&sub); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	_, err := s.db.Exec(`UPDATE subscriptions SET name=?, url=?, update_interval=?, enable=? WHERE id=?`,
		sub.Name, sub.URL, sub.UpdateInterval, sub.Enable, id)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, gin.H{"success": true})
}

// DeleteSubscription 删除订阅
func (s *Server) DeleteSubscription(c *gin.Context) {
	id, _ := strconv.ParseInt(c.Param("id"), 10, 64)
	_, err := s.db.Exec(`DELETE FROM subscriptions WHERE id = ?`, id)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, gin.H{"success": true})
}

// ListCategories 获取分类列表
func (s *Server) ListCategories(c *gin.Context) {
	rows, err := s.db.Query(`SELECT id, name, parent_id, priority, keyword_rules, sort_order, description FROM categories ORDER BY priority`)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	defer rows.Close()
	var cats []models.Category
	for rows.Next() {
		var cat models.Category
		rows.Scan(&cat.ID, &cat.Name, &cat.ParentID, &cat.Priority, &cat.KeywordRules, &cat.SortOrder, &cat.Description)
		cats = append(cats, cat)
	}
	c.JSON(http.StatusOK, gin.H{"data": cats})
}

// CreateCategory 创建分类
func (s *Server) CreateCategory(c *gin.Context) {
	var cat models.Category
	if err := c.ShouldBindJSON(&cat); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	res, err := s.db.Exec(`INSERT INTO categories (name, parent_id, priority, keyword_rules, sort_order, description) VALUES (?, ?, ?, ?, ?, ?)`,
		cat.Name, cat.ParentID, cat.Priority, cat.KeywordRules, cat.SortOrder, cat.Description)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	id, _ := res.LastInsertId()
	c.JSON(http.StatusOK, gin.H{"id": id})
}

// UpdateCategory 更新分类
func (s *Server) UpdateCategory(c *gin.Context) {
	id, _ := strconv.ParseInt(c.Param("id"), 10, 64)
	var cat models.Category
	if err := c.ShouldBindJSON(&cat); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	_, err := s.db.Exec(`UPDATE categories SET name=?, parent_id=?, priority=?, keyword_rules=?, sort_order=?, description=? WHERE id=?`,
		cat.Name, cat.ParentID, cat.Priority, cat.KeywordRules, cat.SortOrder, cat.Description, id)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	s.rulesMgr.Reload()
	c.JSON(http.StatusOK, gin.H{"success": true})
}

// DeleteCategory 删除分类
func (s *Server) DeleteCategory(c *gin.Context) {
	id, _ := strconv.ParseInt(c.Param("id"), 10, 64)
	_, err := s.db.Exec(`DELETE FROM categories WHERE id = ?`, id)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, gin.H{"success": true})
}

// ListDisplayRules 获取显示规则
func (s *Server) ListDisplayRules(c *gin.Context) {
	rows, err := s.db.Query(`SELECT dr.id, dr.category_id, c.name, dr.group_name_override, dr.sort_order, dr.item_sort_order, dr.hide_empty_groups, dr.max_items_per_category, dr.enable 
		FROM display_rule dr LEFT JOIN categories c ON dr.category_id = c.id ORDER BY dr.sort_order`)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	defer rows.Close()
	var rules []map[string]interface{}
	for rows.Next() {
		var id, categoryID int64
		var catName, groupOverride sql.NullString
		var sortOrder, maxItems int
		var itemSortOrder string
		var hideEmpty, enable bool
		rows.Scan(&id, &categoryID, &catName, &groupOverride, &sortOrder, &itemSortOrder, &hideEmpty, &maxItems, &enable)
		rules = append(rules, map[string]interface{}{
			"id": id, "category_id": categoryID, "category_name": catName.String,
			"group_override": groupOverride.String, "sort_order": sortOrder,
			"item_sort_order": itemSortOrder, "hide_empty": hideEmpty,
			"max_items": maxItems, "enable": enable,
		})
	}
	c.JSON(http.StatusOK, gin.H{"data": rules})
}

// UpdateDisplayRules 批量更新显示规则
func (s *Server) UpdateDisplayRules(c *gin.Context) {
	var rules []models.DisplayRule
	if err := c.ShouldBindJSON(&rules); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	tx, _ := s.db.Begin()
	for _, r := range rules {
		_, err := tx.Exec(`UPDATE display_rule SET sort_order=?, item_sort_order=?, hide_empty_groups=?, max_items_per_category=?, enable=? WHERE id=?`,
			r.SortOrder, r.ItemSortOrder, r.HideEmptyGroups, r.MaxItemsPerCategory, r.Enable, r.ID)
		if err != nil {
			tx.Rollback()
			c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
			return
		}
	}
	tx.Commit()
	c.JSON(http.StatusOK, gin.H{"success": true})
}

// GetConfig 获取系统配置
func (s *Server) GetConfig(c *gin.Context) {
	rows, err := s.db.Query(`SELECT group_name, key, value, value_type, description FROM sys_config ORDER BY group_name, key`)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	defer rows.Close()
	configs := make(map[string]interface{})
	for rows.Next() {
		var group, key, value, valType, desc string
		rows.Scan(&group, &key, &value, &valType, &desc)
		if _, ok := configs[group]; !ok {
			configs[group] = make(map[string]interface{})
		}
		configs[group].(map[string]interface{})[key] = map[string]interface{}{
			"value": value, "type": valType, "description": desc,
		}
	}
	c.JSON(http.StatusOK, gin.H{"data": configs})
}

// SaveConfig 保存配置
func (s *Server) SaveConfig(c *gin.Context) {
	var updates map[string]string
	if err := c.ShouldBindJSON(&updates); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	tx, _ := s.db.Begin()
	for key, value := range updates {
		parts := strings.SplitN(key, ".", 2)
		if len(parts) != 2 {
			continue
		}
		group, k := parts[0], parts[1]
		_, err := tx.Exec(`UPDATE sys_config SET value = ? WHERE group_name = ? AND key = ?`, value, group, k)
		if err != nil {
			tx.Rollback()
			c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
			return
		}
	}
	tx.Commit()
	c.JSON(http.StatusOK, gin.H{"success": true})
}

// GetLogs 获取系统日志
func (s *Server) GetLogs(c *gin.Context) {
	level := c.Query("level")
	limit, _ := strconv.Atoi(c.DefaultQuery("limit", "100"))
	query := `SELECT id, level, module, message, created_at FROM system_log WHERE 1=1`
	args := []interface{}{}
	if level != "" {
		query += " AND level = ?"
		args = append(args, level)
	}
	query += " ORDER BY created_at DESC LIMIT ?"
	args = append(args, limit)

	rows, err := s.db.Query(query, args...)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	defer rows.Close()
	var logs []models.SystemLog
	for rows.Next() {
		var l models.SystemLog
		rows.Scan(&l.ID, &l.Level, &l.Module, &l.Message, &l.CreatedAt)
		logs = append(logs, l)
	}
	c.JSON(http.StatusOK, gin.H{"data": logs})
}

// TriggerHotelScan 触发酒店源扫描
func (s *Server) TriggerHotelScan(c *gin.Context) {
	if s.isScanRunning("hotel") {
		c.JSON(http.StatusConflict, gin.H{"error": "酒店源扫描任务已在运行中"})
		return
	}

	rows, err := s.db.Query(`SELECT id, ip_range, port, path FROM hotel_scan_config WHERE enable = 1`)
	if err != nil {
		s.log.Error("获取酒店源扫描配置失败: %v", err)
		c.JSON(http.StatusInternalServerError, gin.H{"error": "获取配置失败"})
		return
	}
	defer rows.Close()

	var configs []struct {
		ID      int64
		IPRange string
		Port    int
		Path    string
	}
	for rows.Next() {
		var cfg struct {
			ID      int64
			IPRange string
			Port    int
			Path    string
		}
		if err := rows.Scan(&cfg.ID, &cfg.IPRange, &cfg.Port, &cfg.Path); err != nil {
			continue
		}
		configs = append(configs, cfg)
	}

	if len(configs) == 0 {
		c.JSON(http.StatusBadRequest, gin.H{"error": "没有启用的酒店源扫描配置"})
		return
	}

	taskID := fmt.Sprintf("hotel_%d", time.Now().Unix())
	go s.runHotelScan(taskID, configs)

	c.JSON(http.StatusOK, gin.H{
		"message": "酒店源扫描任务已启动",
		"task_id": taskID,
	})
}

// TriggerMulticastScan 触发组播源扫描
func (s *Server) TriggerMulticastScan(c *gin.Context) {
	if s.isScanRunning("multicast") {
		c.JSON(http.StatusConflict, gin.H{"error": "组播源扫描任务已在运行中"})
		return
	}

	rows, err := s.db.Query(`SELECT id, interface, address FROM multicast_config WHERE enable = 1`)
	if err != nil {
		s.log.Error("获取组播源扫描配置失败: %v", err)
		c.JSON(http.StatusInternalServerError, gin.H{"error": "获取配置失败"})
		return
	}
	defer rows.Close()

	var configs []struct {
		ID        int64
		Interface string
		Address   string
	}
	for rows.Next() {
		var cfg struct {
			ID        int64
			Interface string
			Address   string
		}
		if err := rows.Scan(&cfg.ID, &cfg.Interface, &cfg.Address); err != nil {
			continue
		}
		configs = append(configs, cfg)
	}

	if len(configs) == 0 {
		c.JSON(http.StatusBadRequest, gin.H{"error": "没有启用的组播源扫描配置"})
		return
	}

	taskID := fmt.Sprintf("multicast_%d", time.Now().Unix())
	go s.runMulticastScan(taskID, configs)

	c.JSON(http.StatusOK, gin.H{
		"message": "组播源扫描任务已启动",
		"task_id": taskID,
	})
}

// TriggerUpdate 手动触发一次完整更新
func (s *Server) TriggerUpdate(c *gin.Context) {
	if s.tester.IsRunning() {
		c.JSON(http.StatusConflict, gin.H{"error": "更新任务已在运行中"})
		return
	}

	go func() {
		s.log.Info("手动触发完整更新...")
		if s.workflowFunc != nil {
			s.workflowFunc()
		} else {
			s.log.Error("工作流函数未设置")
		}
	}()

	c.JSON(http.StatusOK, gin.H{"message": "更新任务已启动"})
}

// GetTaskStatus 获取当前测试任务状态
func (s *Server) GetTaskStatus(c *gin.Context) {
	if s.tester.GetCurrentProgress() != nil {
		c.JSON(http.StatusOK, s.tester.GetCurrentProgress().GetSnapshot())
	} else {
		c.JSON(http.StatusOK, gin.H{"status": "idle"})
	}
}

// 扫描辅助函数

var (
	scanRunningMu sync.Mutex
	scanRunning   = make(map[string]bool)
)

func (s *Server) isScanRunning(scanType string) bool {
	scanRunningMu.Lock()
	defer scanRunningMu.Unlock()
	return scanRunning[scanType]
}

func (s *Server) setScanRunning(scanType string, running bool) {
	scanRunningMu.Lock()
	defer scanRunningMu.Unlock()
	if running {
		scanRunning[scanType] = true
	} else {
		delete(scanRunning, scanType)
	}
}

func (s *Server) runHotelScan(taskID string, configs []struct {
	ID      int64
	IPRange string
	Port    int
	Path    string
}) {
	s.setScanRunning("hotel", true)
	defer s.setScanRunning("hotel", false)

	s.log.Info("开始酒店源扫描，任务ID: %s，配置数: %d", taskID, len(configs))
	startTime := time.Now()

	var totalFound int
	for _, cfg := range configs {
		found := s.scanHotelIPRange(cfg.IPRange, cfg.Port, cfg.Path)
		totalFound += found
		s.db.Exec(`UPDATE hotel_scan_config SET last_scan = ?, found_count = ? WHERE id = ?`,
			time.Now(), found, cfg.ID)
	}

	s.log.Info("酒店源扫描完成，任务ID: %s，共发现 %d 个源，耗时 %v",
		taskID, totalFound, time.Since(startTime))
}

func (s *Server) scanHotelIPRange(ipRange string, port int, path string) int {
	_, ipnet, err := net.ParseCIDR(ipRange)
	if err != nil {
		s.log.Error("解析 CIDR 失败: %s - %v", ipRange, err)
		return 0
	}

	sem := make(chan struct{}, 50)
	var wg sync.WaitGroup
	var foundCount int
	var mu sync.Mutex

	for ip := ipnet.IP.Mask(ipnet.Mask); ipnet.Contains(ip); incIP(ip) {
		wg.Add(1)
		go func(ip net.IP) {
			defer wg.Done()
			sem <- struct{}{}
			defer func() { <-sem }()

			target := fmt.Sprintf("http://%s:%d%s", ip.String(), port, path)
			if s.checkAndSaveSource(target, "hotel_scan") {
				mu.Lock()
				foundCount++
				mu.Unlock()
			}
		}(dupIP(ip))
	}
	wg.Wait()
	return foundCount
}

func incIP(ip net.IP) {
	for j := len(ip) - 1; j >= 0; j-- {
		ip[j]++
		if ip[j] > 0 {
			break
		}
	}
}

func dupIP(ip net.IP) net.IP {
	dup := make(net.IP, len(ip))
	copy(dup, ip)
	return dup
}

func (s *Server) checkAndSaveSource(targetURL, sourceType string) bool {
	client := &http.Client{Timeout: 10 * time.Second}
	resp, err := client.Get(targetURL)
	if err != nil {
		return false
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return false
	}

	buf := make([]byte, 512)
	n, _ := resp.Body.Read(buf)
	content := string(buf[:n])
	if !strings.Contains(content, "#EXTM3U") && !strings.Contains(content, "#EXTINF") {
		return false
	}

	resp.Body.Close()
	resp, _ = client.Get(targetURL)
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)

	sources := source.ParseM3U(string(body), sourceType, targetURL)
	for _, src := range sources {
		s.db.Exec(`INSERT OR IGNORE INTO url_sources (url, name, source_type, created_at) VALUES (?, ?, ?, ?)`,
			src.URL, src.Name, "video", time.Now())
	}
	return len(sources) > 0
}

func (s *Server) runMulticastScan(taskID string, configs []struct {
	ID        int64
	Interface string
	Address   string
}) {
	s.setScanRunning("multicast", true)
	defer s.setScanRunning("multicast", false)

	s.log.Info("开始组播源扫描，任务ID: %s，配置数: %d", taskID, len(configs))
	startTime := time.Now()

	var totalFound int
	for _, cfg := range configs {
		found := s.scanMulticastAddress(cfg.Interface, cfg.Address)
		totalFound += found
		s.db.Exec(`UPDATE multicast_config SET last_scan = ? WHERE id = ?`, time.Now(), cfg.ID)
	}

	s.log.Info("组播源扫描完成，任务ID: %s，共发现 %d 个源，耗时 %v",
		taskID, totalFound, time.Since(startTime))
}

func (s *Server) scanMulticastAddress(iface, address string) int {
	url := fmt.Sprintf("udp://@%s", address)
	if !strings.Contains(address, ":") {
		return 0
	}
	name := fmt.Sprintf("组播_%s", address)
	_, err := s.db.Exec(`INSERT OR IGNORE INTO url_sources (url, name, source_type, created_at) VALUES (?, ?, ?, ?)`,
		url, name, "video", time.Now())
	if err != nil {
		s.log.Debug("插入组播源失败: %v", err)
		return 0
	}
	return 1
}

// HealthCheck 健康检查
func (s *Server) HealthCheck(c *gin.Context) {
	c.JSON(http.StatusOK, gin.H{"status": "healthy"})
}

// ReadyCheck 就绪检查（检查数据库连接等）
func (s *Server) ReadyCheck(c *gin.Context) {
	if err := s.db.Ping(); err != nil {
		c.JSON(http.StatusServiceUnavailable, gin.H{"status": "not ready", "error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, gin.H{"status": "ready"})
}
