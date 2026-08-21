# fun 框架（github.com/cyi-cc/fun）使用文档

> 适用版本：**v1.3.2**（当前最新发布）。基于 fasthttp 的单端点 RPC 框架，
> 自带依赖注入、Guard 鉴权、NDJSON 流式响应、自定义路由与 TypeScript 客户端生成。

## 版本沿革

| 版本 | 要点 |
|---|---|
| v1.1.0 | BindRoute 自定义 GET/POST 路由（回调、健康检查） |
| v1.3.0 | BindRoute 通配符路由 `/prefix/*`，`RouteCtx.Wildcard` 取剩余路径 |
| v1.3.1 | TS 客户端可靠性：所有失败统一归一为 Result 并经过响应拦截器 |
| v1.3.2 | 每请求上下文（request/stream options + `state`）与免基础设施的生成期注册 `BindServiceForGen` |

## 1. 启动与服务注册

```go
func main() {
    f := fun.GetFun()
    f.BindService(&UserSvc{}, &AuthGuard{}) // 服务级 Guard 可选
    f.BindGuard(&LogGuard{})                // 全局 Guard

    cfg := fun.Wired[config.Config]() // 创建/获取单例（触发 DI）
    go f.Start(cfg.ListenPort())      // fasthttp 监听，RPC 只响应 POST /cell
}
```

- 服务结构体嵌入 `fun.Ctx` + 依赖字段（指针结构体字段自动装配）。
- **每请求新建服务实例**并注入依赖，服务内不放共享状态。
- 方法签名四种：`() error`、`(dto) (T, error)`、`(dto) (*fun.Stream, error)`、
  `(dto) (T, *fun.Stream, error)`（首条消息 T + 后续流）。
- 只有导出方法成为端点，注册名 `服务名.方法名`。

## 2. DTO 规则（违反即注册期 panic）

- 允许：定宽整型（int8…int64、uint8…uint64）、string、bool、具名 struct、slice、指针。
- **不支持**：普通 `int`/`uint`、float、map、any/interface、匿名结构体、私有字段。
- 非指针且非 slice 字段必传且非 null；可空字段一律 `*T`；小数用字符串传。
- 枚举：`uint8` 底线 + `Names() []string`（可选 `DisplayNames()`）。
- 响应所有键递归转首字母小写，前端直接 camelCase 取值。

## 3. 线协议与 Result

请求：`POST /cell`，body `{"serviceName","methodName","data","state"}`。

```ts
export type result<T> = {
  id?: string; code?: number; data?: T; msg?: string; status: number
}
```

- `status`：`0` 成功；`1` 框架/协议/基础设施失败；`2` 业务失败（`fun.Error(code,msg)`）；
  `4` 外部请求失败或调用方取消；`5` 明确的外部超时失败。
- 业务错误：`return nil, fun.Error(4001, "登录失败")` —— code/msg 原样透传。
- 成功空 slice 序列化为 `[]`；`data` 为 nil 时整个字段省略。
- `Ctx.State`（map[string]string）请求往返透传；`Ctx.Ip` 已解析客户端 IP。

## 4. Guard（v1.3.2 无变化，推荐用法见 vividai）

```go
func (g *AuthGuard) Guard(ctx fun.Ctx) { /* 校验失败 panic(fun.Error(...)) */ }
f.BindService(&AdminSvc{}, &AuthGuard{})
```

Guard 也是 Box，字段自动注入；panic 被框架兜底转错误响应。
vividai 的用法：**显式端点策略表**（缺省拒绝）+ Guard 从 HttpOnly Cookie 读会话，
并把校验结果经 `RequestCtx.SetUserValue` 传给服务层做对象级授权。

## 5. 自定义路由（v1.3.0+）

```go
f.BindRoute("GET", "/image/*", func(c *fun.RouteCtx) error {
    key := c.Wildcard            // /image/ 之后的剩余路径
    c.RequestCtx.WriteString("…") // 纯文本直写；返回 nil 框架不再写
    return fun.Error(4001, "…")   // 或统一 Result 错误
})
```

- 精确路由优先于通配符；`/cell` 保留；方法大小写不敏感。
- 查询参数与 form 表单合并进 `c.Param(name)`；multipart 不支持（转 base64 走 /cell）。

## 6. 流式响应（NDJSON）

```go
st := &fun.Stream{}
go func() {
    for _, chunk := range chunks {
        if st.Send(chunk) != nil { return } // 连接断开
    }
    st.Close() // 必须关闭
}()
return st, nil
```

`Content-Type: application/x-ndjson`，每行一个 JSON；合法零消息流正常结束；
业务出错在建流前返回普通 Result；`OnClose` 注册清理回调。

## 7. TS 客户端生成与请求上下文（v1.3.2 核心）

```go
f := fun.GetFun()
f.BindServiceForGen(&UserSvc{}) // 生成期专用：只反射注册方法，不装配任何基础设施
fun.SetOutput("./frontend/src/api")
fun.GenCode(fun.GenTs{})
```

- `BindServiceForGen` 不触发 Box 装配，生成命令**不需要数据库/Redis 在运行**。
- 生成确定性：service/method/imports 全部源端排序，重复生成字节一致。
- 产物：`client.ts`（Client + `result<T>`）、每服务一个 `<service>.ts`、DTO/View 类型、
  `fun.ts`（`api.create(url)` 聚合入口，服务属性首字母小写）。

### 每调用选项（v1.3.2）

```ts
export type RequestOptions = { signal?: AbortSignal; state?: Record<string, string> }
export type StreamOptions  = { signal?: AbortSignal; state?: Record<string, string> }

const r = await c.userSvc.profile({ signal: ctrl.signal })
c.chatSvc.chat(dto, msg => {...}, { signal: ctrl.signal })
```

- 调用方可传 `AbortSignal`；**框架自身不设任何请求/连接/空闲超时定时器**，
  超时由调用方或网关（nginx）决定，框架只负责把失败归一为 Result。
- `state` 为每请求字符串字典：请求拦截器可写入（如会话纪元、请求标识），
  响应拦截器经 `context.requestState` 只读快照取回。

### 拦截器（v1.3.2 四参上下文形态）

```ts
c.addRequestInterceptor((svc, m, state) => { state.epoch = myEpoch() })

c.addResponseInterceptor((svc, m, result, context) => {
  // context.requestState: Readonly<Record<string,string>>
  // context.response?: Response   —— 原生 Response（头、状态码可读）
})
```

旧三参签名仍兼容。所有失败（网络、HTTP、HTML、非法 JSON、取消、拦截器异常）
统一归一为 Result 并**必经响应拦截器**，不存在绕过拦截器的错误路径。

## 8. 依赖注入（box.go）

- `fun.Wired[T]()`：按 `*T` 建单例；先注入 `fun:"auto"` 字段（缺则递归创建），
  再调 `New()`（无参；连接类资源在此初始化，失败可 log.Fatalf）。
- 启动顺序：先 `Wired` 基础配置/平台单例，再 `BindService`。

## 9. 常见坑

- DTO 用普通 `int`/float/map → 注册期 panic；用 int64/字符串/指针。
- 非指针字段漏传 → 运行期 "must be a pointer or have a corresponding field"。
- 流式忘记 `Close()` → 客户端挂起；连接断开后 `Send` 返回 error，循环须检查。
- 方法首字母小写 = 不注册；客户端报 method not found。
- vite 代理需重写前缀：`/api/cell → /cell`。
- 生成用 `BindService`（而非 `BindServiceForGen`）会把基础设施拉起来 —— 生成命令请用后者。
