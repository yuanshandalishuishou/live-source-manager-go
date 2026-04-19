-- =============================================================================
-- 数据库名称: live-source.db
-- 描述: 直播源管理工具核心数据库（完整版 v2.1）
-- 新增功能: RTMP推流、归属地/运营商识别、黑白名单、实时进度、正则别名
-- =============================================================================

PRAGMA foreign_keys = ON;
PRAGMA journal_mode = WAL;
PRAGMA synchronous = NORMAL;

-- -----------------------------------------------------------------------------
-- 1. 系统配置表 (sys_config)
-- -----------------------------------------------------------------------------
DROP TABLE IF EXISTS sys_config;
CREATE TABLE sys_config (
    id            INTEGER PRIMARY KEY AUTOINCREMENT,
    group_name    TEXT NOT NULL DEFAULT 'general',
    key           TEXT NOT NULL UNIQUE,
    value         TEXT,
    value_type    TEXT DEFAULT 'string',
    description   TEXT,
    created_at    DATETIME DEFAULT CURRENT_TIMESTAMP,
    updated_at    DATETIME DEFAULT CURRENT_TIMESTAMP
);

-- 插入默认配置项（包含原有 + 新增）
INSERT INTO sys_config (group_name, key, value, value_type, description) VALUES
-- 原有配置 ...
('Network', 'proxy_enabled', 'false', 'bool', '是否启用代理'),
('Network', 'proxy_type', 'socks5', 'string', '代理类型'),
('Network', 'proxy_host', '', 'string', '代理服务器地址'),
('Network', 'proxy_port', '1080', 'int', '代理服务器端口'),
('Network', 'ip_version', 'all', 'string', 'IP版本偏好'),
('Testing', 'timeout', '10', 'int', '流测试超时(秒)'),
('Testing', 'concurrent_threads', '30', 'int', '并发测试线程数'),
('Testing', 'enable_speed_test', 'true', 'bool', '是否启用速度测试'),
('Output', 'filename', 'live.m3u', 'string', '输出文件名'),
('Output', 'group_by', 'category', 'string', '分组依据'),
('Output', 'max_sources_per_channel', '3', 'int', '每频道最多保留源数量'),
('Filter', 'max_latency', '5000', 'int', '最大延迟(毫秒)'),
('Filter', 'min_bitrate', '100', 'int', '最低比特率(kbps)'),
('Filter', 'min_resolution', '720p', 'string', '最低分辨率'),
('Filter', 'max_resolution', '4k', 'string', '最高分辨率'),
-- 新增：归属地与运营商过滤配置
('Filter', 'location', '', 'string', '接口归属地过滤，逗号分隔'),
('Filter', 'isp', '', 'string', '运营商过滤，逗号分隔'),
('Filter', 'origin_type_prefer', 'local,subscribe', 'string', '接口来源偏好排序'),
-- 新增：RTMP 配置
('RTMP', 'open_rtmp', 'true', 'bool', '是否开启 RTMP 推流'),
('RTMP', 'nginx_http_port', '8080', 'int', 'Nginx HTTP 服务端口'),
('RTMP', 'nginx_rtmp_port', '1935', 'int', 'Nginx RTMP 服务端口'),
('RTMP', 'rtmp_idle_timeout', '300', 'int', '空闲停止推流超时(秒)'),
('RTMP', 'rtmp_max_streams', '10', 'int', '最大并发推流数量'),
('RTMP', 'rtmp_transcode_mode', 'copy', 'string', '转码模式: copy/auto'),
-- 原有其他配置 ...
('EPG', 'update_interval', '12', 'int', 'EPG更新间隔(小时)'),
('EPG', 'include_epg_url', 'true', 'bool', '是否包含 EPG URL'),
('WebServer', 'port', '23456', 'int', 'Web 管理界面端口'),
('System', 'admin_username', 'admin', 'string', '默认管理员用户名');

-- -----------------------------------------------------------------------------
-- 2. 用户表 (users)
-- -----------------------------------------------------------------------------
DROP TABLE IF EXISTS users;
CREATE TABLE users (
    id             INTEGER PRIMARY KEY AUTOINCREMENT,
    username       TEXT NOT NULL UNIQUE,
    password_hash  TEXT NOT NULL,
    is_admin       BOOLEAN DEFAULT 0,
    created_at     DATETIME DEFAULT CURRENT_TIMESTAMP,
    last_login     DATETIME,
    is_active      BOOLEAN DEFAULT 1
);

INSERT INTO users (username, password_hash, is_admin) VALUES 
('admin', '$2a$10$N9qo8uLOickgx2ZMRZoMy.MqrqbKqVjK5NqYVhK7Q8Z9X3pQ6wXyO', 1);

-- -----------------------------------------------------------------------------
-- 3. 直播源文件表 (live_sources)
-- -----------------------------------------------------------------------------
DROP TABLE IF EXISTS live_sources;
CREATE TABLE live_sources (
    id              INTEGER PRIMARY KEY AUTOINCREMENT,
    name            TEXT NOT NULL,
    location        TEXT NOT NULL,
    location_type   TEXT NOT NULL DEFAULT 'url',
    enable          BOOLEAN DEFAULT 1,
    last_download   DATETIME,
    download_status TEXT DEFAULT 'pending',
    http_status     INTEGER,
    retry_count     INTEGER DEFAULT 0,
    created_at      DATETIME DEFAULT CURRENT_TIMESTAMP,
    updated_at      DATETIME DEFAULT CURRENT_TIMESTAMP
);

-- -----------------------------------------------------------------------------
-- 4. 原始直播源条目表 (url_sources)
-- -----------------------------------------------------------------------------
DROP TABLE IF EXISTS url_sources;
CREATE TABLE url_sources (
    id              INTEGER PRIMARY KEY AUTOINCREMENT,
    live_source_id  INTEGER NOT NULL,
    url             TEXT NOT NULL,
    name            TEXT,
    tvg_id          TEXT,
    tvg_logo        TEXT,
    group_title     TEXT,
    catchup         TEXT,
    catchup_days    INTEGER,
    user_agent      TEXT,
    raw_attributes  TEXT,
    source_type     TEXT DEFAULT 'video',
    created_at      DATETIME DEFAULT CURRENT_TIMESTAMP,
    UNIQUE(url, name),
    FOREIGN KEY(live_source_id) REFERENCES live_sources(id) ON DELETE CASCADE
);

CREATE INDEX idx_url_sources_live_source_id ON url_sources(live_source_id);
CREATE INDEX idx_url_sources_name ON url_sources(name);

-- -----------------------------------------------------------------------------
-- 5. 分类表 (categories)
-- -----------------------------------------------------------------------------
DROP TABLE IF EXISTS categories;
CREATE TABLE categories (
    id            INTEGER PRIMARY KEY AUTOINCREMENT,
    name          TEXT NOT NULL UNIQUE,
    parent_id     INTEGER,
    priority      INTEGER DEFAULT 100,
    keyword_rules TEXT,                    -- JSON: 支持正则 {"type":"regex","patterns":[...]}
    sort_order    INTEGER DEFAULT 0,
    description   TEXT,
    created_at    DATETIME DEFAULT CURRENT_TIMESTAMP,
    FOREIGN KEY(parent_id) REFERENCES categories(id) ON DELETE SET NULL
);

-- 插入默认分类
INSERT INTO categories (name, priority, keyword_rules) VALUES
('央视频道', 1, '{"type":"regex","patterns":["^CCTV[0-9]+","央视.*","CGTN.*"]}'),
('卫视频道', 10, '{"type":"keyword","keywords":["卫视","TV"]}'),
('影视频道', 15, '{"type":"keyword","keywords":["电影","影院","影视"]}'),
('体育频道', 15, '{"type":"keyword","keywords":["体育","NBA","足球"]}'),
('其他频道', 100, '{"type":"keyword","keywords":[]}');

-- -----------------------------------------------------------------------------
-- 6. 频道别名表 (channel_alias) -- 新增
-- -----------------------------------------------------------------------------
DROP TABLE IF EXISTS channel_alias;
CREATE TABLE channel_alias (
    id            INTEGER PRIMARY KEY AUTOINCREMENT,
    pattern       TEXT NOT NULL,           -- 正则表达式模式
    target_name   TEXT NOT NULL,           -- 替换后的频道名称
    priority      INTEGER DEFAULT 100,
    enable        BOOLEAN DEFAULT 1,
    description   TEXT,
    created_at    DATETIME DEFAULT CURRENT_TIMESTAMP
);

-- 示例数据
INSERT INTO channel_alias (pattern, target_name, priority, description) VALUES
('^CCTV[\\s\\-]*1[^0-9]?.*', 'CCTV-1 综合', 1, 'CCTV1 别名'),
('^CCTV[\\s\\-]*13.*', 'CCTV-13 新闻', 1, 'CCTV13 别名'),
('.*凤凰卫视.*', '凤凰卫视中文台', 10, '凤凰卫视别名');

-- -----------------------------------------------------------------------------
-- 7. 源-分类关联表 (source_categories)
-- -----------------------------------------------------------------------------
DROP TABLE IF EXISTS source_categories;
CREATE TABLE source_categories (
    source_id   INTEGER NOT NULL,
    category_id INTEGER NOT NULL,
    PRIMARY KEY (source_id, category_id),
    FOREIGN KEY(source_id) REFERENCES url_sources_passed(id) ON DELETE CASCADE,
    FOREIGN KEY(category_id) REFERENCES categories(id) ON DELETE CASCADE
);

-- -----------------------------------------------------------------------------
-- 8. 验证通过的直播源表 (url_sources_passed)
-- -----------------------------------------------------------------------------
DROP TABLE IF EXISTS url_sources_passed;
CREATE TABLE url_sources_passed (
    id                  INTEGER PRIMARY KEY AUTOINCREMENT,
    url                 TEXT NOT NULL UNIQUE,
    name                TEXT NOT NULL,
    tvg_id              TEXT,
    tvg_logo            TEXT,
    group_title         TEXT,
    catchup             TEXT,
    catchup_days        INTEGER,
    user_agent          TEXT,
    source_type         TEXT DEFAULT 'video',
    raw_attributes      TEXT,
    live_source_id      INTEGER,
    epg_id              TEXT,
    epg_name            TEXT,
    epg_logo            TEXT,
    -- 测试结果
    status              TEXT DEFAULT 'unknown',
    response_time_ms    INTEGER,
    resolution          TEXT,
    bitrate             INTEGER,
    video_codec         TEXT,
    audio_codec         TEXT,
    frame_rate          REAL,
    download_speed      REAL,
    last_checked        DATETIME,
    fail_count          INTEGER DEFAULT 0,
    test_status         TEXT,
    error_message       TEXT,
    -- 新增：归属地与运营商
    location            TEXT,               -- 归属地（如"北京"）
    isp                 TEXT,               -- 运营商（如"电信"）
    -- 扩展属性
    extra_attrs         TEXT,
    created_at          DATETIME DEFAULT CURRENT_TIMESTAMP,
    updated_at          DATETIME DEFAULT CURRENT_TIMESTAMP,
    FOREIGN KEY(live_source_id) REFERENCES live_sources(id) ON DELETE SET NULL
);

CREATE INDEX idx_passed_status ON url_sources_passed(status);
CREATE INDEX idx_passed_name ON url_sources_passed(name);
CREATE INDEX idx_passed_last_checked ON url_sources_passed(last_checked);
CREATE INDEX idx_passed_location ON url_sources_passed(location);
CREATE INDEX idx_passed_isp ON url_sources_passed(isp);

-- -----------------------------------------------------------------------------
-- 9. 显示规则表 (display_rule)
-- -----------------------------------------------------------------------------
DROP TABLE IF EXISTS display_rule;
CREATE TABLE display_rule (
    id                    INTEGER PRIMARY KEY AUTOINCREMENT,
    category_id           INTEGER NOT NULL,
    group_name_override   TEXT,
    sort_order            INTEGER DEFAULT 0,
    item_sort_order       TEXT DEFAULT '1',
    hide_empty_groups     BOOLEAN DEFAULT 0,
    max_items_per_category INTEGER DEFAULT 0,
    filter_resolution_min TEXT,
    enable                BOOLEAN DEFAULT 1,
    created_at            DATETIME DEFAULT CURRENT_TIMESTAMP,
    updated_at            DATETIME DEFAULT CURRENT_TIMESTAMP,
    FOREIGN KEY(category_id) REFERENCES categories(id) ON DELETE CASCADE
);

-- -----------------------------------------------------------------------------
-- 10. 白名单表 (whitelist) -- 新增
-- -----------------------------------------------------------------------------
DROP TABLE IF EXISTS whitelist;
CREATE TABLE whitelist (
    id          INTEGER PRIMARY KEY AUTOINCREMENT,
    pattern     TEXT NOT NULL,
    target_type TEXT DEFAULT 'url',
    enable      BOOLEAN DEFAULT 1,
    priority    INTEGER DEFAULT 0,
    description TEXT,
    created_at  DATETIME DEFAULT CURRENT_TIMESTAMP
);

-- -----------------------------------------------------------------------------
-- 11. 黑名单表 (blacklist) -- 新增
-- -----------------------------------------------------------------------------
DROP TABLE IF EXISTS blacklist;
CREATE TABLE blacklist (
    id          INTEGER PRIMARY KEY AUTOINCREMENT,
    pattern     TEXT NOT NULL,
    target_type TEXT DEFAULT 'url',
    enable      BOOLEAN DEFAULT 1,
    description TEXT,
    created_at  DATETIME DEFAULT CURRENT_TIMESTAMP
);

-- -----------------------------------------------------------------------------
-- 12. RTMP 推流管理表 (rtmp_streams) -- 新增
-- -----------------------------------------------------------------------------
DROP TABLE IF EXISTS rtmp_streams;
CREATE TABLE rtmp_streams (
    id               INTEGER PRIMARY KEY AUTOINCREMENT,
    source_id        INTEGER NOT NULL,
    stream_status    TEXT DEFAULT 'stopped',
    push_url         TEXT,
    hls_url          TEXT,
    last_push_time   DATETIME,
    idle_seconds     INTEGER DEFAULT 0,
    error_message    TEXT,
    created_at       DATETIME DEFAULT CURRENT_TIMESTAMP,
    FOREIGN KEY(source_id) REFERENCES url_sources_passed(id) ON DELETE CASCADE
);

-- -----------------------------------------------------------------------------
-- 13. 测试进度表 (test_progress) -- 新增
-- -----------------------------------------------------------------------------
DROP TABLE IF EXISTS test_progress;
CREATE TABLE test_progress (
    id               INTEGER PRIMARY KEY AUTOINCREMENT,
    task_id          TEXT NOT NULL,
    total_sources    INTEGER DEFAULT 0,
    tested_sources   INTEGER DEFAULT 0,
    success_count    INTEGER DEFAULT 0,
    failed_count     INTEGER DEFAULT 0,
    current_source   TEXT,
    status           TEXT DEFAULT 'running',
    started_at       DATETIME DEFAULT CURRENT_TIMESTAMP,
    updated_at       DATETIME DEFAULT CURRENT_TIMESTAMP
);

-- -----------------------------------------------------------------------------
-- 14. EPG 节目表 (epg_program)
-- -----------------------------------------------------------------------------
DROP TABLE IF EXISTS epg_program;
CREATE TABLE epg_program (
    id          INTEGER PRIMARY KEY AUTOINCREMENT,
    epg_id      TEXT NOT NULL,
    start_time  DATETIME NOT NULL,
    end_time    DATETIME NOT NULL,
    title       TEXT NOT NULL,
    description TEXT,
    category    TEXT,
    language    TEXT,
    icon        TEXT,
    created_at  DATETIME DEFAULT CURRENT_TIMESTAMP
);

CREATE INDEX idx_epg_epg_id ON epg_program(epg_id);
CREATE INDEX idx_epg_start_time ON epg_program(start_time);

-- -----------------------------------------------------------------------------
-- 15. 测试历史表 (test_history)
-- -----------------------------------------------------------------------------
DROP TABLE IF EXISTS test_history;
CREATE TABLE test_history (
    id                INTEGER PRIMARY KEY AUTOINCREMENT,
    source_id         INTEGER NOT NULL,
    test_time         DATETIME DEFAULT CURRENT_TIMESTAMP,
    success           BOOLEAN NOT NULL,
    response_time_ms  INTEGER,
    status_code       INTEGER,
    resolution        TEXT,
    bitrate           INTEGER,
    error_message     TEXT,
    FOREIGN KEY(source_id) REFERENCES url_sources_passed(id) ON DELETE CASCADE
);

-- -----------------------------------------------------------------------------
-- 16. 系统日志表 (system_log)
-- -----------------------------------------------------------------------------
DROP TABLE IF EXISTS system_log;
CREATE TABLE system_log (
    id          INTEGER PRIMARY KEY AUTOINCREMENT,
    level       TEXT NOT NULL,
    module      TEXT,
    message     TEXT NOT NULL,
    details     TEXT,
    created_at  DATETIME DEFAULT CURRENT_TIMESTAMP
);

-- -----------------------------------------------------------------------------
-- 17. 订阅管理表 (subscriptions)
-- -----------------------------------------------------------------------------
DROP TABLE IF EXISTS subscriptions;
CREATE TABLE subscriptions (
    id              INTEGER PRIMARY KEY AUTOINCREMENT,
    name            TEXT NOT NULL,
    url             TEXT NOT NULL,
    update_interval INTEGER DEFAULT 3600,
    last_update     DATETIME,
    enable          BOOLEAN DEFAULT 1,
    created_at      DATETIME DEFAULT CURRENT_TIMESTAMP
);

-- -----------------------------------------------------------------------------
-- 18. 酒店源扫描配置表 (hotel_scan_config)
-- -----------------------------------------------------------------------------
DROP TABLE IF EXISTS hotel_scan_config;
CREATE TABLE hotel_scan_config (
    id          INTEGER PRIMARY KEY AUTOINCREMENT,
    ip_range    TEXT NOT NULL,
    port        INTEGER DEFAULT 80,
    path        TEXT DEFAULT '/iptv.m3u',
    enable      BOOLEAN DEFAULT 1,
    last_scan   DATETIME,
    found_count INTEGER DEFAULT 0
);

-- -----------------------------------------------------------------------------
-- 19. 组播源配置表 (multicast_config)
-- -----------------------------------------------------------------------------
DROP TABLE IF EXISTS multicast_config;
CREATE TABLE multicast_config (
    id          INTEGER PRIMARY KEY AUTOINCREMENT,
    interface   TEXT NOT NULL,
    address     TEXT NOT NULL,
    enable      BOOLEAN DEFAULT 1,
    last_scan   DATETIME
);

-- =============================================================================
-- 触发器: 自动更新 updated_at 字段
-- =============================================================================
CREATE TRIGGER IF NOT EXISTS update_sys_config_updated_at
AFTER UPDATE ON sys_config
BEGIN
    UPDATE sys_config SET updated_at = CURRENT_TIMESTAMP WHERE id = NEW.id;
END;

CREATE TRIGGER IF NOT EXISTS update_live_sources_updated_at
AFTER UPDATE ON live_sources
BEGIN
    UPDATE live_sources SET updated_at = CURRENT_TIMESTAMP WHERE id = NEW.id;
END;

CREATE TRIGGER IF NOT EXISTS update_passed_updated_at
AFTER UPDATE ON url_sources_passed
BEGIN
    UPDATE url_sources_passed SET updated_at = CURRENT_TIMESTAMP WHERE id = NEW.id;
END;

CREATE TRIGGER IF NOT EXISTS update_display_rule_updated_at
AFTER UPDATE ON display_rule
BEGIN
    UPDATE display_rule SET updated_at = CURRENT_TIMESTAMP WHERE id = NEW.id;
END;
