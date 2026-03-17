package db

// 建表 SQL，用于创建新数据库
const SchemaSQL = `
CREATE TABLE IF NOT EXISTS sys_config (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    group_name TEXT NOT NULL,
    key TEXT NOT NULL,
    value TEXT,
    description TEXT,
    created_at DATETIME DEFAULT CURRENT_TIMESTAMP,
    updated_at DATETIME DEFAULT CURRENT_TIMESTAMP,
    UNIQUE(group_name, key)
);

CREATE TABLE IF NOT EXISTS live_sources (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    name TEXT,
    source_path TEXT NOT NULL,
    enabled BOOLEAN DEFAULT 1,
    source_type TEXT CHECK(source_type IN ('online','local','custom')),
    created_at DATETIME DEFAULT CURRENT_TIMESTAMP,
    updated_at DATETIME DEFAULT CURRENT_TIMESTAMP
);

CREATE TABLE IF NOT EXISTS url_sources (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    live_source_id INTEGER NOT NULL,
    url TEXT NOT NULL,
    name TEXT,
    source_type TEXT,
    created_at DATETIME DEFAULT CURRENT_TIMESTAMP,
    UNIQUE(live_source_id, url),
    FOREIGN KEY (live_source_id) REFERENCES live_sources(id) ON DELETE CASCADE
);

CREATE TABLE IF NOT EXISTS categories (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    name TEXT NOT NULL UNIQUE,
    parent_id INTEGER,
    description TEXT,
    sort_order INTEGER DEFAULT 0,
    created_at DATETIME DEFAULT CURRENT_TIMESTAMP,
    FOREIGN KEY (parent_id) REFERENCES categories(id) ON DELETE SET NULL
);

CREATE TABLE IF NOT EXISTS source_categories (
    source_id INTEGER NOT NULL,
    category_id INTEGER NOT NULL,
    PRIMARY KEY (source_id, category_id),
    FOREIGN KEY (source_id) REFERENCES sources(id) ON DELETE CASCADE,
    FOREIGN KEY (category_id) REFERENCES categories(id) ON DELETE CASCADE
);

CREATE TABLE IF NOT EXISTS sources (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    live_source_id INTEGER,
    url_source_id INTEGER,
    url TEXT NOT NULL UNIQUE,
    name TEXT,
    source_type TEXT,
    status TEXT DEFAULT 'unknown',
    last_checked DATETIME,
    enabled BOOLEAN DEFAULT 1,
    created_at DATETIME DEFAULT CURRENT_TIMESTAMP,
    updated_at DATETIME DEFAULT CURRENT_TIMESTAMP,
    epg_id TEXT,
    epg_name TEXT,
    epg_logo TEXT,
    group_title TEXT,
    request_headers TEXT,
    extra_attrs TEXT,
    FOREIGN KEY (live_source_id) REFERENCES live_sources(id) ON DELETE SET NULL,
    FOREIGN KEY (url_source_id) REFERENCES url_sources(id) ON DELETE SET NULL
);

CREATE TABLE IF NOT EXISTS display_rule (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    category_id INTEGER NOT NULL UNIQUE,
    sort_order INTEGER DEFAULT 0,
    group_title_override TEXT,
    item_sort_by TEXT DEFAULT 'name',
    item_sort_order TEXT DEFAULT 'asc',
    enabled BOOLEAN DEFAULT 1,
    created_at DATETIME DEFAULT CURRENT_TIMESTAMP,
    updated_at DATETIME DEFAULT CURRENT_TIMESTAMP,
    FOREIGN KEY (category_id) REFERENCES categories(id) ON DELETE CASCADE
);

CREATE TABLE IF NOT EXISTS epg_program (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    epg_id TEXT NOT NULL,
    start_time DATETIME NOT NULL,
    end_time DATETIME NOT NULL,
    title TEXT NOT NULL,
    description TEXT
);

CREATE TABLE IF NOT EXISTS test_history (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    source_id INTEGER NOT NULL,
    test_time DATETIME DEFAULT CURRENT_TIMESTAMP,
    success BOOLEAN NOT NULL,
    response_time_ms INTEGER,
    status_code INTEGER,
    error_message TEXT,
    resolution TEXT,
    bitrate TEXT,
    FOREIGN KEY (source_id) REFERENCES sources(id) ON DELETE CASCADE
);

CREATE TABLE IF NOT EXISTS system_log (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    level TEXT NOT NULL CHECK(level IN ('INFO','WARN','ERROR')),
    module TEXT,
    message TEXT NOT NULL,
    detail TEXT,
    created_at DATETIME DEFAULT CURRENT_TIMESTAMP
);

-- 索引
CREATE INDEX IF NOT EXISTS idx_sources_status ON sources(status);
CREATE INDEX IF NOT EXISTS idx_sources_last_checked ON sources(last_checked);
CREATE INDEX IF NOT EXISTS idx_sources_source_type ON sources(source_type);
CREATE INDEX IF NOT EXISTS idx_test_history_source ON test_history(source_id);
CREATE INDEX IF NOT EXISTS idx_epg_program_epg_id ON epg_program(epg_id);
CREATE INDEX IF NOT EXISTS idx_log_level ON system_log(level);
`
