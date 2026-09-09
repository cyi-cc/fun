package fun

import (
	"context"
	"fmt"
	"reflect"
	"sync"
	"sync/atomic"
	"time"

	"github.com/valyala/fasthttp"
)

type Fun struct {
	methods        map[string]methodInfo
	routes         map[string]boundRoute     // 自定义路由："GET /path" → 绑定的处理器与 Guard（精确匹配）
	wildcardRoutes map[string][]wildcardRoute // 通配路由，按 HTTP 方法
	boxes          *sync.Map                 // 依赖容器：reflect.Type → boxEntry（单例或粘性错误）
	guards         []*any                    // 全局 Guard
	serviceGuards  map[string][]*any         // 服务级 Guard，按服务名
	bodyLimit      int                       // 请求体上限（字节）；0 = fasthttp 默认 4MB

	readTimeout    time.Duration // 读超时，默认 60s（slowloris 防线）
	writeTimeout   time.Duration // 写超时，默认 0 不限制（避免掐断长流式响应）
	idleTimeout    time.Duration // keep-alive 空闲超时，默认 120s
	maxConcurrency int           // 最大并发连接数；0 = 不限制

	corsOrigins  map[string]struct{}   // CORS 来源白名单（小写比较）；nil/空表示未开启

	server  atomic.Pointer[fasthttp.Server]
	started atomic.Bool

	mu sync.Mutex // 注册与依赖装配互斥：保护 methods/routes/boxes 的写入
}

// SetBodyLimit 设置请求体上限（字节），须在 Start 前调用。
// multipart 上传等大请求体的自定义路由需要时设置；0 或负数恢复默认。
func (f *Fun) SetBodyLimit(n int) {
	f.mustNotStarted("SetBodyLimit")
	if n < 0 {
		n = 0
	}
	f.bodyLimit = n
}

// SetTimeouts 配置服务器超时，须在 Start 前调用；单项传 0 表示不限制。
// 默认 ReadTimeout 60s / IdleTimeout 120s / WriteTimeout 不限制
func (f *Fun) SetTimeouts(read, write, idle time.Duration) {
	f.mustNotStarted("SetTimeouts")
	f.mu.Lock()
	defer f.mu.Unlock()
	f.readTimeout, f.writeTimeout, f.idleTimeout = read, write, idle
}

// SetMaxConcurrency 配置最大并发连接数（0 = 不限制），须在 Start 前调用。
// 防御慢 handler 堆积 goroutine 打爆内存
func (f *Fun) SetMaxConcurrency(n int) {
	f.mustNotStarted("SetMaxConcurrency")
	f.mu.Lock()
	defer f.mu.Unlock()
	f.maxConcurrency = n
}

// wildcardRoute 通配符路由（BindRoute path 以 "/*" 结尾注册）：
// prefix 如 "/image"，匹配 prefix 与 prefix 下任意子路径
type wildcardRoute struct {
	prefix string
	route  boundRoute
}

var (
	errorType  = reflect.TypeFor[error]()
	streamType = reflect.TypeFor[*Stream]()
)

var (
	fun   *Fun
	funMu sync.Mutex
)

// methodInfo 已注册方法的元信息
type methodInfo struct {
	serviceType reflect.Type // 服务值类型（非指针），每请求新建实例
	methodIndex int          // 方法在实例上的反射索引
	dtoType     reflect.Type // DTO 参数类型，无参数时为 nil
	isStream    bool         // 返回签名带 *Stream，响应走 NDJSON 流式
}

func newFun() *Fun {
	return &Fun{
		methods:        map[string]methodInfo{},
		routes:         map[string]boundRoute{},
		wildcardRoutes: map[string][]wildcardRoute{},
		boxes:          &sync.Map{},
		serviceGuards:  map[string][]*any{},
		readTimeout:    60 * time.Second,
		idleTimeout:    120 * time.Second,
		// writeTimeout 保持 0：流式响应可能长时间推送，写超时会掐断连接
	}
}

func New() *Fun {
	f := newFun()
	funMu.Lock()
	if fun == nil {
		fun = f
	}
	funMu.Unlock()
	return f
}

// GetFun 返回默认 Fun 实例，未初始化时自动创建（并发安全）
func GetFun() *Fun {
	funMu.Lock()
	defer funMu.Unlock()
	if fun == nil {
		fun = newFun()
	}
	return fun
}

// BindService 注册服务，要求传入指向结构体的指针
// 方法签名约束：
//   - 参数：最多一个，且必须是 struct（作为 DTO）
//   - 返回值：只支持四种签名——(error)、(T, error)、(stream, error)、(T, stream, error)
//
// guardList 为该服务绑定的 Guard，方法调用前按注册顺序执行。
// 依赖装配失败（New() 返回 error）以 error 返回，由调用方决定退出或降级；
// 用法错误（非结构体指针、非法签名）仍为 panic，等价编译期检查
func (f *Fun) BindService(service any, guardList ...Guard) error {
	t, name := serviceType(service)
	f.mu.Lock()
	defer f.mu.Unlock()
	f.mustNotStarted("BindService")
	if err := boxWired(service, f); err != nil {
		return err
	}

	serviceGuards := make([]*any, 0, len(guardList))
	for _, guard := range guardList {
		checkGuard(guard)
		g, err := serviceGuardWired(guard, f)
		if err != nil {
			return fmt.Errorf("fun: wire guard %T: %w", guard, err)
		}
		serviceGuards = append(serviceGuards, g)
	}
	f.serviceGuards[name] = serviceGuards
	f.bindServiceMethods(t, name)
	return nil
}

// BindServiceForGen registers service metadata for code generation without
// constructing runtime dependencies or guards.
func (f *Fun) BindServiceForGen(service any) {
	t, name := serviceType(service)
	f.bindServiceMethods(t, name)
}

func serviceType(service any) (reflect.Type, string) {
	t := reflect.TypeOf(service)
	if t == nil || t.Kind() != reflect.Ptr || t.Elem().Kind() != reflect.Struct {
		panic("fun: BindService requires a pointer to a struct")
	}
	name := t.Elem().Name()
	if name == "" {
		panic("fun: BindService requires a named type")
	}
	return t, name
}

func (f *Fun) bindServiceMethods(t reflect.Type, name string) {
	for m := range t.Methods() {
		m := m
		// Ctx 命名持有 *fasthttp.RequestCtx（非嵌入），服务方法集只含业务方法，无需过滤提升方法
		mt := m.Type

		// 参数：接收者 + 最多一个 DTO（NumIn() 含接收者），DTO 必须是 struct
		if mt.NumIn() > 2 {
			panic(fmt.Sprintf("fun: method %s has more than one parameter", m.Name))
		}
		var dtoType reflect.Type
		if mt.NumIn() == 2 {
			dtoType = mt.In(1)
			if dtoType.Kind() != reflect.Struct {
				panic(fmt.Sprintf("fun: method %s parameter must be a struct", m.Name))
			}
			checkType(dtoType)
		}

		// 返回值只支持四种签名：error / (T, error) / (stream, error) / (T, stream, error)
		isStream := false
		switch mt.NumOut() {
		case 1:
			// 情况 1：func(...) error
			if mt.Out(0) != errorType {
				panic(fmt.Sprintf("fun: method %s must return (error), (T, error), (stream, error) or (T, stream, error)", m.Name))
			}
		case 2:
			// 情况 2：func(...) (T, error) 或 func(...) (*Stream, error)
			if mt.Out(1) != errorType {
				panic(fmt.Sprintf("fun: method %s last return value must be error", m.Name))
			}
			isStream = mt.Out(0) == streamType
		case 3:
			// 情况 3：func(...) (T, *Stream, error)
			if mt.Out(2) != errorType {
				panic(fmt.Sprintf("fun: method %s last return value must be error", m.Name))
			}
			if mt.Out(1) != streamType {
				panic(fmt.Sprintf("fun: method %s second return value must be *Stream", m.Name))
			}
			isStream = true
		default:
			panic(fmt.Sprintf("fun: method %s must return (error), (T, error), (stream, error) or (T, stream, error)", m.Name))
		}

		// 注册到 "ServiceName.MethodName"
		f.methods[name+"."+m.Name] = methodInfo{
			serviceType: t.Elem(),
			methodIndex: m.Index,
			dtoType:     dtoType,
			isStream:    isStream,
		}
	}
}

// BindGuard 注册全局 Guard，对所有服务生效
func (f *Fun) BindGuard(guard Guard) error {
	checkGuard(guard)
	f.mu.Lock()
	defer f.mu.Unlock()
	f.mustNotStarted("BindGuard")
	g, err := serviceGuardWired(guard, f)
	if err != nil {
		return fmt.Errorf("fun: wire guard %T: %w", guard, err)
	}
	f.guards = append(f.guards, g)
	return nil
}

// callGuard 按全局 → 服务级顺序执行 Guard，首个非 nil error 短路返回
func (f *Fun) callGuard(c *Ctx, serviceName string) error {
	for _, g := range f.guards {
		if err := (*g).(Guard).Guard(*c); err != nil {
			return err
		}
	}
	for _, g := range f.serviceGuards[serviceName] {
		if err := (*g).(Guard).Guard(*c); err != nil {
			return err
		}
	}
	return nil
}

// mustNotStarted 注册期 API 在 Start 后调用即 panic：
// 运行期对 methods/routes 等注册表的读取不持锁，晚注册与并发请求是数据竞争
func (f *Fun) mustNotStarted(op string) {
	if f.started.Load() {
		panic("fun: " + op + " must be called before Start")
	}
}

// Start 在指定端口启动 HTTP 服务（阻塞）。
// 默认 ReadTimeout 60s、IdleTimeout 120s（slowloris 防线，SetTimeouts 可调），
// WriteTimeout 默认不限制，长流式响应不会被掐断。
// 优雅停机用 Shutdown；重复 Start panic
func (f *Fun) Start(port uint16) {
	f.StartOn(fmt.Sprintf(":%d", port))
}

// StartOn 在指定地址（":8080"、"127.0.0.1:9000" 等）启动服务，语义同 Start
func (f *Fun) StartOn(addr string) {
	f.mu.Lock()
	if f.started.Swap(true) {
		f.mu.Unlock()
		panic("fun: Start already called")
	}
	srv := f.newServer()
	f.server.Store(srv)
	f.mu.Unlock()
	if err := srv.ListenAndServe(addr); err != nil {
		panic(err.Error())
	}
}

// Shutdown 优雅停机：停止接受新连接，等待在途请求（含流式响应）完成或 ctx 超时。
// 未启动或已停机时为空操作
func (f *Fun) Shutdown(ctx context.Context) error {
	if s := f.server.Load(); s != nil {
		return s.ShutdownWithContext(ctx)
	}
	return nil
}

func (f *Fun) newServer() *fasthttp.Server {
	srv := &fasthttp.Server{Handler: f.handle}
	if f.bodyLimit > 0 {
		srv.MaxRequestBodySize = f.bodyLimit
	}
	if f.readTimeout > 0 {
		srv.ReadTimeout = f.readTimeout
	}
	if f.writeTimeout > 0 {
		srv.WriteTimeout = f.writeTimeout
	}
	if f.idleTimeout > 0 {
		srv.IdleTimeout = f.idleTimeout
	}
	if f.maxConcurrency > 0 {
		srv.Concurrency = f.maxConcurrency
	}
	return srv
}
