package fun

import (
	"net"
	"strings"

	"github.com/valyala/fasthttp"
)

// clientIP 解析客户端真实 IP。
// 优先级：X-Forwarded-For > X-Real-IP > RemoteAddr。
// 部署在反向代理（nginx 等）后时由代理写入这两个头；
// 直连无代理头时回退到连接对端地址
func clientIP(ctx *fasthttp.RequestCtx) string {
	// 1. X-Forwarded-For 取最后一个非空段：
	// 该段由离服务最近的一层代理追加，是代理链中最可信的一段
	if ip := lastNonEmpty(string(ctx.Request.Header.Peek("X-Forwarded-For"))); ip != "" {
		return toLoopback(ip)
	}

	// 2. X-Real-IP（通常由 nginx 设置）
	if ip := strings.TrimSpace(string(ctx.Request.Header.Peek("X-Real-IP"))); ip != "" {
		return toLoopback(ip)
	}

	// 3. 回退到连接对端地址；无对端或未指定地址（0.0.0.0，测试/直驱场景）按本机处理
	if remote := ctx.RemoteIP(); remote != nil && !remote.IsUnspecified() {
		return toLoopback(remote.String())
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

// toLoopback 回环地址统一返回 127.0.0.1，其余原样返回
func toLoopback(ip string) string {
	if parsed := net.ParseIP(ip); parsed != nil && parsed.IsLoopback() {
		return "127.0.0.1"
	}
	return ip
}
