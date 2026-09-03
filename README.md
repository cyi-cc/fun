# fun

基于 [fasthttp](https://github.com/valyala/fasthttp) 的单端点 RPC 框架。业务请求统一走
`POST /cell`，按 `ServiceName.MethodName` 反射调用；自带依赖注入、Guard 鉴权、
NDJSON 流式响应、自定义路由与 TypeScript 客户端生成。

## 特性

- **单端点 RPC**：`POST /cell`，方法签名 `(error)`、`(T, error)`、`(stream, error)`、`(T, stream, error)`
- **依赖注入**：`fun.Wired[T]()` 建单例，`auto` 标签字段递归装配，`New()` 初始化连接资源
- **Guard 鉴权**：全局 + 服务级中间件，panic 兜底转统一错误响应
- **NDJSON 流式**：`*fun.Stream` 逐行推送，支持首条消息 + 后续流
- **自定义路由**（v1.3.0+）：`BindRoute` 注册 GET/POST 回调、健康检查、通配符路径
- **请求体上限控制**（v1.3.3+）：`SetBodyLimit` 支持大体积 multipart 上传
- **TypeScript 客户端生成**：`BindServiceForGen` + `GenCode(fun.GenTs{})`，免基础设施即可生成，产物带 `result<T>` 归一化错误与拦截器

## 快速开始

```go
func main() {
    f := fun.GetFun()
    f.BindService(&UserSvc{})   // 服务结构体嵌入 fun.Ctx，导出方法即 RPC 端点

    cfg := fun.Wired[config.Config]()
    go f.Start(cfg.ListenPort()) // fasthttp 监听，RPC 只响应 POST /cell
}
```

## 文档

完整使用文档见 [docs/README.zh.md](docs/README.zh.md)：DTO 规则、线协议与 Result、
Guard、自定义路由、流式响应、TS 客户端生成与常见坑。
