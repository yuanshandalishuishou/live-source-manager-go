package generator

import (
    "database/sql"
    "fmt"
    "os"
    "sort"
    "strings"
)

// GenerateM3U 根据 display_rule 和 sources 生成 M3U 文件
func GenerateM3U(db *sql.DB, outPath string) error {
    // 获取所有启用的 active 源
    rows, err := db.Query(`
        SELECT s.id, s.name, s.url, s.epg_id, s.epg_name, s.epg_logo, s.group_title, 
               s.request_headers, s.extra_attrs
        FROM sources s
        WHERE s.enabled=1 AND s.status='active'
        ORDER BY s.group_title, s.name
    `)
    if err != nil {
        return err
    }
    defer rows.Close()

    // 收集源并按分类分组（实际应按 display_rule 处理）
    // 简化：直接按 group_title 分组输出
    groups := make(map[string][]SourceInfo)
    for rows.Next() {
        var s SourceInfo
        if err := rows.Scan(&s.ID, &s.Name, &s.URL, &s.EPGID, &s.EPGName, &s.EPGLogo, &s.GroupTitle, &s.RequestHeaders, &s.ExtraAttrs); err != nil {
            continue
        }
        groups[s.GroupTitle] = append(groups[s.GroupTitle], s)
    }

    f, err := os.Create(outPath)
    if err != nil {
        return err
    }
    defer f.Close()

    fmt.Fprintln(f, "#EXTM3U")

    // 按规则排序组（这里简单按组名排序）
    var groupNames []string
    for g := range groups {
        groupNames = append(groupNames, g)
    }
    sort.Strings(groupNames)

    for _, g := range groupNames {
        // 可选：从 display_rule 获取自定义组名
        groupTitle := g
        for _, s := range groups[g] {
            attrs := []string{}
            if s.EPGID != "" {
                attrs = append(attrs, fmt.Sprintf("tvg-id=\"%s\"", s.EPGID))
            }
            if s.EPGName != "" {
                attrs = append(attrs, fmt.Sprintf("tvg-name=\"%s\"", s.EPGName))
            }
            if s.EPGLogo != "" {
                attrs = append(attrs, fmt.Sprintf("tvg-logo=\"%s\"", s.EPGLogo))
            }
            if groupTitle != "" {
                attrs = append(attrs, fmt.Sprintf("group-title=\"%s\"", groupTitle))
            }
            attrStr := strings.Join(attrs, " ")
            fmt.Fprintf(f, "#EXTINF:-1 %s,%s\n", attrStr, s.Name)
            fmt.Fprintln(f, s.URL)
        }
    }
    return nil
}

type SourceInfo struct {
    ID            int64
    Name          string
    URL           string
    EPGID         string
    EPGName       string
    EPGLogo       string
    GroupTitle    string
    RequestHeaders string
    ExtraAttrs    string
}
