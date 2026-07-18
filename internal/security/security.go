// Package security implements URL safety checks for live-source parsing and validation.
//
// Two gates are provided:
//   - IsStaticSafe: the narrow gate used during the PARSE stage. It only rejects
//     invalid schemes and SSRF (private/loopback/link-local/metadata hosts). It deliberately
//     does NOT do DNS resolution or content-policy checks, because reachability is decided
//     later by the StreamTester, and "server-unreachable but user-watchable" sources must not
//     be silently dropped.
//   - ValidateURL / IsSafeURL: the full gate used when a user manually adds a source. It
//     additionally performs DNS resolution (rejecting internal resolutions), XSS / command
//     injection / path traversal scanning, and overseas-streaming / IP-blacklist checks.
package security

import (
	"context"
	"net"
	"net/url"
	"regexp"
	"strings"
	"sync"
	"time"
)

// AllowedSchemes are the protocols permitted for a live source.
var AllowedSchemes = map[string]bool{
	"http": true, "https": true, "rtmp": true, "rtsp": true, "rtp": true,
}

// BlockedSchemes are protocols that are never permitted.
var BlockedSchemes = map[string]bool{
	"file": true, "data": true, "javascript": true, "vbscript": true, "jar": true,
	"ftp": true, "chrome": true, "chrome-extension": true, "edge": true,
	"safari-extension": true, "view-source": true, "about": true,
}

// DefaultDomainBlacklist is the built-in self-reference blacklist.
var DefaultDomainBlacklist = map[string]bool{
	"localhost": true, "127.0.0.1": true, "0.0.0.0": true, "255.255.255.255": true,
	"internal.example.com": true,
}

// PrivateIPPrefixes are IPv4 ranges treated as private/internal.
var PrivateIPPrefixes = []string{
	"10.", "172.16.", "172.17.", "172.18.", "172.19.", "172.20.", "172.21.",
	"172.22.", "172.23.", "172.24.", "172.25.", "172.26.", "172.27.", "172.28.",
	"172.29.", "172.30.", "172.31.", "192.168.", "127.", "169.254.",
}

var (
	mu                sync.RWMutex
	domainBlacklist   = map[string]bool{}
	domainWhitelist   = map[string]bool{}
	overseasStreaming = map[string]bool{}
	knownOverseasCIDR = []string{
		"74.125.0.0/16", "172.217.0.0/16", "216.58.0.0/16", "108.177.0.0/17",
		"52.0.0.0/8", "104.16.0.0/12", "172.64.0.0/13", "23.0.0.0/12",
		"96.0.0.0/12", "184.24.0.0/13",
	}
)

func init() {
	for k := range DefaultDomainBlacklist {
		domainBlacklist[k] = true
	}
	// Known overseas streaming domains (Cybersecurity Law article 38).
	for _, d := range []string{
		"youtube.com", "www.youtube.com", "m.youtube.com", "youtu.be", "ytimg.com",
		"googlevideo.com", "youtube.googleapis.com", "netflix.com", "www.netflix.com",
		"nflxvideo.net", "nflximg.net", "nflxext.com", "nflxso.net", "hbomax.com",
		"www.hbomax.com", "hbogo.com", "max.com", "www.max.com", "disneyplus.com",
		"www.disneyplus.com", "dssott.com", "disney.api.edge.bamgrid.com",
		"primevideo.com", "www.primevideo.com", "amazonaws.com", "amazonvideo.com",
		"tv.apple.com", "apple.com", "hulu.com", "www.hulu.com", "hulustream.com",
		"paramountplus.com", "cbs.com", "cbsi.com", "peacocktv.com", "nbc.com",
		"spotify.com", "open.spotify.com", "tidal.com", "pandora.com", "deezer.com",
		"twitch.tv", "www.twitch.tv", "jtvnw.net", "vimeo.com", "vimeocdn.com",
		"dailymotion.com", "dmcdn.net",
	} {
		overseasStreaming[d] = true
	}
}

// SetDomainBlacklist replaces the blacklist (used by tests / config).
func SetDomainBlacklist(domains []string) {
	mu.Lock()
	defer mu.Unlock()
	domainBlacklist = map[string]bool{}
	for k := range DefaultDomainBlacklist {
		domainBlacklist[k] = true
	}
	for _, d := range domains {
		d = strings.ToLower(strings.TrimSpace(d))
		if d != "" {
			domainBlacklist[d] = true
		}
	}
}

// SetDomainWhitelist replaces the whitelist (default-deny mode when non-empty).
func SetDomainWhitelist(domains []string) {
	mu.Lock()
	defer mu.Unlock()
	domainWhitelist = map[string]bool{}
	for _, d := range domains {
		d = strings.ToLower(strings.TrimSpace(d))
		if d != "" {
			domainWhitelist[d] = true
		}
	}
}

func isPrivateIP(host string) bool {
	for _, p := range PrivateIPPrefixes {
		if strings.HasPrefix(host, p) {
			return true
		}
	}
	if ip := net.ParseIP(host); ip != nil {
		return ip.IsPrivate() || ip.IsLoopback() || ip.IsLinkLocalUnicast() || ip.IsLinkLocalMulticast()
	}
	return false
}

func isValidHost(host string) bool {
	host = strings.ToLower(host)
	re := regexp.MustCompile(`^[a-z0-9:._\-\[\]]+$`)
	if !re.MatchString(host) {
		return false
	}
	if net.ParseIP(host) != nil {
		return true
	}
	return strings.Contains(host, ".")
}

func isWhitelisted(host string, wl map[string]bool) bool {
	host = strings.ToLower(host)
	if wl[host] {
		return true
	}
	parts := strings.Split(host, ".")
	for i := 1; i < len(parts); i++ {
		wild := "*." + strings.Join(parts[i:], ".")
		if wl[wild] {
			return true
		}
	}
	return false
}

func isOverseasStreaming(host string) bool {
	host = strings.ToLower(host)
	if overseasStreaming[host] {
		return true
	}
	for known := range overseasStreaming {
		if strings.HasSuffix(host, "."+known) {
			return true
		}
	}
	return false
}

func checkIPBlacklist(host string) string {
	ip := net.ParseIP(host)
	if ip == nil {
		return ""
	}
	for _, cidr := range knownOverseasCIDR {
		_, netC, err := net.ParseCIDR(cidr)
		if err == nil && netC.Contains(ip) {
			return "IP 在境外 CDN 黑名单中: " + host + " (" + cidr + ")"
		}
	}
	return ""
}

var dnsResolver = &net.Resolver{}

func checkDNSResolution(host string) string {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	addrs, err := dnsResolver.LookupIPAddr(ctx, host)
	if err != nil {
		return "DNS 解析失败: " + host + "（域名可能不存在或网络不可达）"
	}
	for _, a := range addrs {
		ip := a.IP
		if ip.IsPrivate() || ip.IsLoopback() {
			return "DNS 解析到内网地址: " + host + " → " + ip.String()
		}
	}
	return ""
}

// IsStaticSafe is the narrow parse-stage gate. Returns (safe, reason, category).
// category ∈ {ok, scheme, host, ssrf}.
func IsStaticSafe(raw string) (bool, string, string) {
	if raw == "" || strings.TrimSpace(raw) == "" {
		return false, "URL 为空", "host"
	}
	clean := strings.Split(strings.TrimSpace(raw), "|")[0]
	clean = strings.Split(clean, "#")[0]
	parsed, err := url.Parse(clean)
	if err != nil {
		return false, "URL 解析失败: " + err.Error(), "host"
	}
	scheme := strings.ToLower(parsed.Scheme)
	if scheme == "" {
		return false, "URL 缺少协议 scheme", "scheme"
	}
	if !AllowedSchemes[scheme] {
		return false, "不支持的协议: " + scheme + "（仅支持 http/https/rtmp/rtsp/rtp）", "scheme"
	}
	host := strings.ToLower(parsed.Hostname())
	if host == "" {
		host = strings.ToLower(parsed.Host)
	}
	host = strings.TrimSpace(host)
	if host == "" {
		return false, "URL 缺少主机地址", "host"
	}
	if !isValidHost(host) {
		return false, "无效的主机地址格式: " + host, "host"
	}
	// SSRF protection: reject self-referencing / internal hosts.
	if host == "localhost" || strings.HasSuffix(host, ".localhost") ||
		strings.HasSuffix(host, ".local") || strings.HasSuffix(host, ".internal") {
		return false, "拒绝自引用主机(SSRF): " + host, "ssrf"
	}
	if isPrivateIP(host) {
		return false, "私有/内网 IP 被拒绝(SSRF): " + host, "ssrf"
	}
	return true, "", "ok"
}

// ValidateResult is the outcome of ValidateURL.
type ValidateResult struct {
	Valid         bool
	Safe          bool
	Reason        string
	NormalizedURL string
}

// ValidateURL performs the full safety check (parse-stage gate + DNS + content policy).
func ValidateURL(raw string) ValidateResult {
	res := ValidateResult{Valid: false, Safe: false, NormalizedURL: raw}
	if raw == "" || strings.TrimSpace(raw) == "" {
		res.Reason = "URL 为空"
		return res
	}
	clean := strings.Split(strings.TrimSpace(raw), "|")[0]
	clean = strings.Split(clean, "#")[0]
	parsed, err := url.Parse(clean)
	if err != nil {
		res.Reason = "URL 解析失败: " + err.Error()
		return res
	}
	scheme := strings.ToLower(parsed.Scheme)
	if scheme == "" {
		res.Reason = "URL 缺少协议 scheme"
		return res
	}
	if BlockedSchemes[scheme] {
		res.Reason = "不安全的协议: " + scheme
		return res
	}
	if !AllowedSchemes[scheme] {
		res.Reason = "不支持的协议: " + scheme + "（仅支持 http/https/rtmp/rtsp/rtp）"
		return res
	}
	host := strings.ToLower(parsed.Hostname())
	if host == "" {
		host = strings.ToLower(parsed.Host)
	}
	if host == "" {
		res.Reason = "URL 缺少主机地址"
		return res
	}
	if !isValidHost(host) {
		res.Reason = "无效的主机地址格式: " + host
		return res
	}
	if domainBlacklistCheck(host) {
		res.Reason = "域名在黑名单中: " + host
		return res
	}
	mu.RLock()
	wl := domainWhitelist
	mu.RUnlock()
	if len(wl) > 0 && !isWhitelisted(host, wl) {
		res.Reason = "域名不在白名单中: " + host + "（白名单默认拒绝模式已启用）"
		return res
	}
	if isOverseasStreaming(host) {
		res.Reason = "域名在境外流媒体拦截列表中: " + host + "（《网络安全法》第38条）"
		return res
	}
	if isPrivateIP(host) {
		res.Reason = "私有 IP 地址被拒绝: " + host
		return res
	}
	if reason := checkIPBlacklist(host); reason != "" {
		res.Reason = reason
		return res
	}
	if reason := checkDNSResolution(host); reason != "" {
		res.Reason = reason
		return res
	}
	if reason := checkXSS(raw); reason != "" {
		res.Reason = reason
		return res
	}
	if reason := checkCommandInjection(parsed.Path + "?" + parsed.RawQuery); reason != "" {
		res.Reason = reason
		return res
	}
	if reason := checkPathTraversal(parsed.Path); reason != "" {
		res.Reason = reason
		return res
	}
	res.Valid = true
	res.Safe = true
	res.NormalizedURL = SanitizeURL(clean)
	return res
}

// IsSafeURL is a shortcut returning (safe, reason).
func IsSafeURL(raw string) (bool, string) {
	r := ValidateURL(raw)
	return r.Safe, r.Reason
}

func domainBlacklistCheck(host string) bool {
	mu.RLock()
	defer mu.RUnlock()
	return domainBlacklist[strings.ToLower(host)]
}

var (
	xssPatterns = []*regexp.Regexp{
		regexp.MustCompile(`(?i)<script[^>]*>`),
		regexp.MustCompile(`(?i)</script>`),
		regexp.MustCompile(`(?i)javascript\s*:`),
		regexp.MustCompile(`(?i)\bon(?:abort|blur|change|click|contextmenu|dblclick|drag|dragend|dragenter|dragleave|dragover|drop|error|focus|input|keydown|keypress|keyup|load|mousedown|mouseenter|mouseleave|mousemove|mouseout|mouseover|mouseup|reset|resize|scroll|select|submit|unload|wheel)\s*=`),
		regexp.MustCompile(`(?i)<iframe[^>]*>`),
		regexp.MustCompile(`(?i)<embed[^>]*>`),
		regexp.MustCompile(`(?i)<object[^>]*>`),
		regexp.MustCompile(`(?i)alert\s*\(`),
		regexp.MustCompile(`(?i)eval\s*\(`),
		regexp.MustCompile(`(?i)document\.cookie`),
		regexp.MustCompile(`(?i)window\.location`),
		regexp.MustCompile(`(?i)expression\s*\(`),
		regexp.MustCompile(`(?i)vbscript\s*:`),
		regexp.MustCompile(`(?i)data\s*:`),
	}
	cmdInjectionPatterns = []*regexp.Regexp{
		regexp.MustCompile(`\$\{.*?\}`),
		regexp.MustCompile("`[^`]*`"),
		regexp.MustCompile(`\$\(.*?\)`),
		regexp.MustCompile(`\|\s*[a-z]+`),
		regexp.MustCompile(`;\s*[a-z]+`),
	}
	pathTraversalPatterns = []*regexp.Regexp{
		regexp.MustCompile(`\.\./\.\.`),
		regexp.MustCompile(`\.\.\\\.\.`),
		regexp.MustCompile(`(?i)%2e%2e%2f`),
		regexp.MustCompile(`(?i)%2e%2e/`),
	}
)

func checkXSS(u string) string {
	for _, p := range xssPatterns {
		if m := p.FindString(u); m != "" {
			if len(m) > 50 {
				m = m[:50]
			}
			return "检测到 XSS 注入 payload: '" + m + "'"
		}
	}
	return ""
}

func checkCommandInjection(s string) string {
	for _, p := range cmdInjectionPatterns {
		if m := p.FindString(s); m != "" {
			if len(m) > 50 {
				m = m[:50]
			}
			return "检测到命令注入 payload: '" + m + "'"
		}
	}
	return ""
}

func checkPathTraversal(p string) string {
	for _, pat := range pathTraversalPatterns {
		if m := pat.FindString(p); m != "" {
			if len(m) > 50 {
				m = m[:50]
			}
			return "检测到路径遍历 payload: '" + m + "'"
		}
	}
	return ""
}

// SanitizeURL normalizes a URL: lowercases host, collapses double slashes in path, and
// strips unsafe query parameters (cmd/exec/eval/callback/jsonp/...).
func SanitizeURL(raw string) string {
	parsed, err := url.Parse(raw)
	if err != nil {
		return raw
	}
	parsed.Host = strings.ToLower(parsed.Host)
	path := parsed.Path
	for strings.Contains(path, "//") {
		path = strings.ReplaceAll(path, "//", "/")
	}
	q := parsed.Query()
	unsafe := map[string]bool{
		"cmd": false, "exec": false, "command": false, "shell": false,
		"debug": false, "eval": false, "callback": false, "jsonp": false,
	}
	for k := range q {
		if _, ok := unsafe[strings.ToLower(k)]; ok {
			q.Del(k)
		}
	}
	parsed.RawQuery = q.Encode()
	parsed.Path = path
	parsed.Fragment = ""
	return parsed.String()
}
