package utils

import (
	"net"
	"net/url"
	"strings"
)

// IsValidURL 检查字符串是否为有效的 HTTP/HTTPS URL。
func IsValidURL(rawURL string) bool {
	u, err := url.Parse(rawURL)
	if err != nil {
		return false
	}
	return u.Scheme == "http" || u.Scheme == "https"
}

// CheckIPVersion 检查 URL 中的 IP 版本是否符合偏好。
// preference: "ipv4", "ipv6", "all"
func CheckIPVersion(rawURL, preference string) bool {
	if preference == "all" {
		return true
	}
	u, err := url.Parse(rawURL)
	if err != nil {
		return true // 无法解析则默认允许通过
	}
	hostname := u.Hostname()
	if hostname == "" {
		return true
	}

	ip := net.ParseIP(hostname)
	if ip == nil {
		// 域名，无法判断版本，默认允许
		return true
	}

	isIPv4 := ip.To4() != nil
	if preference == "ipv4" && !isIPv4 {
		return false
	}
	if preference == "ipv6" && isIPv4 {
		return false
	}
	return true
}

// ExtractHost 从 URL 中提取主机名（不含端口）。
func ExtractHost(rawURL string) string {
	u, err := url.Parse(rawURL)
	if err != nil {
		return ""
	}
	return u.Hostname()
}
