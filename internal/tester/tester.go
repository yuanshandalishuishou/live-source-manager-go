package tester

import (
    "database/sql"
    "net/http"
    "sync"
    "time"

    "video-source-manager/internal/config"
)

// TestAllPending 测试所有未测试或过期的源
func TestAllPending(db *sql.DB, cfg *config.Config) {
    // 查询需要测试的源：status 不是 active 或 last_checked 超过一定时间
    rows, err := db.Query(`
        SELECT id, url, request_headers FROM sources 
        WHERE enabled=1 AND (status != 'active' OR last_checked IS NULL OR last_checked < datetime('now', '-1 day'))
    `)
    if err != nil {
        return
    }
    defer rows.Close()

    var wg sync.WaitGroup
    sem := make(chan struct{}, cfg.TestConcurrency)

    for rows.Next() {
        var id int64
        var url string
        var headersJSON sql.NullString
        if err := rows.Scan(&id, &url, &headersJSON); err != nil {
            continue
        }
        wg.Add(1)
        go func(id int64, url string, headersJSON sql.NullString) {
            defer wg.Done()
            sem <- struct{}{}
            defer func() { <-sem }()

            testSingleSource(db, id, url, headersJSON, cfg)
        }(id, url, headersJSON)
    }
    wg.Wait()
}

func testSingleSource(db *sql.DB, id int64, url string, headersJSON sql.NullString, cfg *config.Config) {
    client := http.Client{Timeout: cfg.TestTimeout}
    req, err := http.NewRequest("HEAD", url, nil)
    if err != nil {
        updateTestResult(db, id, false, 0, err)
        return
    }
    // 可解析 headersJSON 设置请求头
    if headersJSON.Valid && headersJSON.String != "" {
        // 简单实现：假设格式如 {"User-Agent":"xxx"}，需要解析
        // 这里略
    }

    start := time.Now()
    resp, err := client.Do(req)
    duration := time.Since(start).Milliseconds()
    if err != nil {
        updateTestResult(db, id, false, duration, err)
        return
    }
    defer resp.Body.Close()
    success := resp.StatusCode >= 200 && resp.StatusCode < 400
    updateTestResult(db, id, success, duration, nil)
    // 可进一步探测分辨率等（调用 ffprobe）
}

func updateTestResult(db *sql.DB, sourceID int64, success bool, respTime int64, err error) {
    status := "inactive"
    if success {
        status = "active"
    }
    // 更新 sources 表
    _, _ = db.Exec(`
        UPDATE sources SET status=?, last_checked=?, updated_at=CURRENT_TIMESTAMP
        WHERE id=?
    `, status, time.Now(), sourceID)

    // 插入测试历史
    errorMsg := ""
    if err != nil {
        errorMsg = err.Error()
    }
    _, _ = db.Exec(`
        INSERT INTO test_history (source_id, success, response_time_ms, error_message)
        VALUES (?, ?, ?, ?)
    `, sourceID, success, respTime, errorMsg)
}
