package fun

import (
	"fmt"
	"strings"

	"github.com/valyala/fasthttp"
)

// RouteHandler 自定义 HTTP 路由处理器。
//
// 返回 error 时框架统一输出错误响应；返回 nil 视为已自行写回响应——
// 可直接操作 RouteCtx.RequestCtx 完全自定义状态码与内容
// （如支付回调要求的纯文本 "success" 应答）。
type RouteHandler func(ctx *RouteCtx) error

// boundRoute 路由绑定的处理器与其 Guard
type boundRoute struct {
	handler RouteHandler
	guards  []*any
}

// RouteCtx 自定义路由上下文：Data 合并了 URL 查询参数与 POST 表单参数（表单优先），
// 支付回调等第三方以 form-urlencoded 回调的场景可直接 Param 取值。
// Wildcard 为通配符路由（/prefix/*）匹配到的剩余路径（不含前导 "/"）。
// 独立于服务内嵌的 Ctx：后者辅助方法刻意全小写以防混入 RPC 方法集，路由不复用该类型。
type RouteCtx struct {
	RequestCtx *fasthttp.RequestCtx
	Data       map[string]string
	Wildcard   string
}

// Param 取查询/表单参数，不存在返回空串
func (c *RouteCtx) Param(name string) string {
	return c.Data[name]
}

// BindRoute 注册自定义路由（方法大小写不敏感；path 精确匹配，或以 "/*" 结尾做前缀通配），
// 用于 GET 直链、健康检查、支付回调等无法走 POST /cell RPC 的场景。
//
//   - guardList 为该路由绑定的 Guard，处理器前按注册顺序执行：
//     Guard 收到的 Ctx.State 已合并 URL 查询与表单参数（token 放查询参数即可鉴权），
//     返回 error 时短路——处理器不执行，error 走统一 Result 错误响应
//   - path 必须以 "/" 开头；/cell 为 RPC 保留路径，不可注册
//   - 通配符形式如 "/image/*"：匹配 "/image/a/b.png" 等任意子路径，
//     匹配到的剩余路径（去掉前导 "/"，如 "a/b.png"）经 RouteCtx.Wildcard 取出
//   - Guard 依赖装配失败以 error 返回；非法参数与重复注册仍 panic
//   - 须在 Start 前完成注册
func (f *Fun) BindRoute(method, path string, handler RouteHandler, guardList ...Guard) error {
	if handler == nil {
		panic("fun: BindRoute handler cannot be nil")
	}
	method = strings.ToUpper(strings.TrimSpace(method))
	if method == "" {
		panic("fun: BindRoute method cannot be empty")
	}
	if !strings.HasPrefix(path, "/") {
		panic(fmt.Sprintf("fun: BindRoute path %q must start with '/'", path))
	}
	if path == "/cell" || path == "/cell/*" {
		panic("fun: /cell is reserved for RPC")
	}

	f.mu.Lock()
	defer f.mu.Unlock()
	f.mustNotStarted("BindRoute")

	br := boundRoute{handler: handler}
	for _, guard := range guardList {
		checkGuard(guard)
		g, err := serviceGuardWired(guard, f)
		if err != nil {
			return fmt.Errorf("fun: wire route guard %T: %w", guard, err)
		}
		br.guards = append(br.guards, g)
	}

	if prefix, ok := strings.CutSuffix(path, "/*"); ok {
		if prefix == "" || strings.HasSuffix(prefix, "/") {
			panic(fmt.Sprintf("fun: BindRoute wildcard path %q invalid (no trailing '/' allowed before /*)", path))
		}
		for _, r := range f.wildcardRoutes[method] {
			if r.prefix == prefix {
				panic(fmt.Sprintf("fun: route %s %s/* already bound", method, prefix))
			}
		}
		f.wildcardRoutes[method] = append(f.wildcardRoutes[method], wildcardRoute{prefix: prefix, route: br})
		return nil
	}
	key := method + " " + path
	if _, exists := f.routes[key]; exists {
		panic(fmt.Sprintf("fun: route %s already bound", key))
	}
	f.routes[key] = br
	return nil
}

// handleRoute 执行自定义路由：合并查询与表单参数（application/x-www-form-urlencoded），
// 先按序执行路由 Guard（State 即合并参数，token 放查询参数即可鉴权），
// 任一 Guard 返回 error 则短路；处理器返回 error 时按统一 Result 格式输出错误响应
func (f *Fun) handleRoute(fastCtx *fasthttp.RequestCtx, r boundRoute, wildcard string) {
	data := map[string]string{}
	fastCtx.QueryArgs().VisitAll(func(k, v []byte) {
		data[string(k)] = string(v)
	})
	fastCtx.PostArgs().VisitAll(func(k, v []byte) {
		data[string(k)] = string(v)
	})
	ctx := &Ctx{RequestCtx: fastCtx, Ip: clientIP(fastCtx), State: data}
	for _, g := range r.guards {
		if err := (*g).(Guard).Guard(*ctx); err != nil {
			ctx.sendError(err)
			return
		}
	}
	if err := r.handler(&RouteCtx{RequestCtx: fastCtx, Data: data, Wildcard: wildcard}); err != nil {
		ctx.sendError(err)
	}
}
