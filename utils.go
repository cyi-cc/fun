package fun

import (
	"net"
	"net/http"
	"strings"
)

// getIP 获取客户端真实 IP
// 优先级：X-Forwarded-For > X-Real-IP > RemoteAddr
func getIP(r *http.Request) string {
	// 1. 优先获取真实 IP（多层代理时取最后一个非空段）
	if ip := lastNonEmpty(r.Header.Get("X-Forwarded-For")); ip != "" {
		return toLoopback(ip)
	}

	// 2. X-Real-IP（通常由 Nginx 设置）
	if ip := strings.TrimSpace(r.Header.Get("X-Real-IP")); ip != "" {
		return toLoopback(ip)
	}

	// 3. 最终回退到 RemoteAddr（兼容带端口、IPv6 方括号、无端口）
	if ip := hostOf(r.RemoteAddr); ip != "" {
		return toLoopback(ip)
	}

	return "127.0.0.1"
}

// lastNonEmpty 取 X-Forwarded-For 中最后一个非空段
// X-Forwarded-For: client, proxy1, proxy2
func lastNonEmpty(xff string) string {
	parts := strings.Split(xff, ",")
	for i := len(parts) - 1; i >= 0; i-- {
		if ip := strings.TrimSpace(parts[i]); ip != "" {
			return ip
		}
	}
	return ""
}

// hostOf 从 RemoteAddr 中提取 IP 部分
// "203.0.113.9:4567" → "203.0.113.9"，"[::1]:4567" → "::1"，
// "198.51.100.88"（无端口）→ 原样返回
func hostOf(remoteAddr string) string {
	raw := strings.TrimSpace(remoteAddr)
	host, _, err := net.SplitHostPort(raw)
	if err == nil && host != "" {
		return host
	}
	return raw
}

// toLoopback 回环地址统一返回 127.0.0.1，其余原样返回
func toLoopback(ip string) string {
	if parsed := net.ParseIP(ip); parsed != nil && parsed.IsLoopback() {
		return "127.0.0.1"
	}
	return ip
}
