package fun

import (
	"strings"

	"github.com/valyala/fasthttp"
)

const (
	corsMaxAge = "86400" // 预检结果缓存 24h，减少浏览器 OPTIONS 探测
)

// CORS 配置跨域来源白名单，须在 Start 前调用；作用于全部端点（/cell、自定义路由与通配路由）。
// 按需放行：仅列出的来源会被放行——请求 Origin 命中白名单时回显该来源并允许
// 携带凭据（Cookie），未命中则不附加 CORS 头（浏览器侧自然拦截）。
//
//	f.CORS("https://a.com", "https://b.com")
//
// 预检请求（OPTIONS 且带 Access-Control-Request-Method 头）直接以 204 应答，
// 不进入路由与 /cell 处理。
func (f *Fun) CORS(origins ...string) {
	f.mustNotStarted("CORS")
	whitelist := make(map[string]struct{}, len(origins))
	for _, o := range origins {
		if o != "" {
			whitelist[strings.ToLower(o)] = struct{}{}
		}
	}
	f.corsOrigins = whitelist
}

// corsEnabled CORS 是否已配置
func (f *Fun) corsEnabled() bool {
	return len(f.corsOrigins) > 0
}

// handleCors 处理 CORS：Origin 命中白名单的请求附加跨域响应头，
// 预检请求（OPTIONS + Access-Control-Request-Method）在此直接应答并返回 true。
func (f *Fun) handleCors(fastCtx *fasthttp.RequestCtx) bool {
	if !f.corsEnabled() {
		return false
	}
	origin := string(fastCtx.Request.Header.Peek("Origin"))
	if origin == "" {
		return false // 同源请求与服务器到服务器调用，无跨域语义
	}
	if _, ok := f.corsOrigins[strings.ToLower(origin)]; !ok {
		return false // 未命中白名单：不加 CORS 头，交给浏览器拦截
	}

	h := &fastCtx.Response.Header
	h.Set("Access-Control-Allow-Origin", origin)
	h.Set("Access-Control-Allow-Credentials", "true")
	// 放行结果随 Origin 变化，防中间层缓存错配
	h.Add("Vary", "Origin")

	if !fastCtx.IsOptions() || fastCtx.Request.Header.Peek("Access-Control-Request-Method") == nil {
		return false // 实际请求：头已附加，继续正常路由
	}

	// 预检请求：回显浏览器声明的目标方法与请求头，24h 内同请求免预检
	h.Set("Access-Control-Allow-Methods", "GET, POST, OPTIONS")
	if reqHeaders := fastCtx.Request.Header.Peek("Access-Control-Request-Headers"); len(reqHeaders) > 0 {
		h.Set("Access-Control-Allow-Headers", string(reqHeaders))
	} else {
		h.Set("Access-Control-Allow-Headers", "Content-Type")
	}
	h.Set("Access-Control-Max-Age", corsMaxAge)
	fastCtx.SetStatusCode(fasthttp.StatusNoContent)
	return true
}
