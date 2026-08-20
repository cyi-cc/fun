package fun

import (
	"fmt"
	"reflect"
	"sync"

	"github.com/valyala/fasthttp"
)

type Fun struct {
	methods       map[string]methodInfo
	routes        map[string]RouteHandler // 自定义路由："GET /path" → 处理器
	boxes         *sync.Map              // 依赖容器：reflect.Type → reflect.Value
	guards        []*any                 // 全局 Guard
	serviceGuards map[string][]*any      // 服务级 Guard，按服务名
}

var (
	errorType  = reflect.TypeFor[error]()
	streamType = reflect.TypeFor[*Stream]()
)

var fun *Fun

// methodInfo 已注册方法的元信息
type methodInfo struct {
	serviceType reflect.Type // 服务值类型（非指针），每请求新建实例
	methodIndex int          // 方法在实例上的反射索引
	dtoType     reflect.Type // DTO 参数类型，无参数时为 nil
	isStream    bool         // 返回签名带 *Stream，走 RequestStreamType
}

func New() *Fun {
	f := &Fun{
		methods:       map[string]methodInfo{},
		routes:        map[string]RouteHandler{},
		boxes:         &sync.Map{},
		serviceGuards: map[string][]*any{},
	}
	if fun == nil {
		fun = f
	}
	return f
}

// GetFun 返回默认 Fun 实例，未初始化时自动创建
func GetFun() *Fun {
	if fun == nil {
		fun = New()
	}
	return fun
}

// BindService 注册服务，要求传入指向结构体的指针
// 方法签名约束：
//   - 参数：最多一个，且必须是 struct（作为 DTO）
//   - 返回值：只支持四种签名——(error)、(T, error)、(stream, error)、(T, stream, error)
//
// guardList 为该服务绑定的 Guard，方法调用前按注册顺序执行
func (f *Fun) BindService(service any, guardList ...Guard) {
	t := reflect.TypeOf(service)
	// 必须是指针指向的结构体，匿名类型无法注册
	if t.Kind() != reflect.Ptr || t.Elem().Kind() != reflect.Struct {
		panic("fun: BindService requires a pointer to a struct")
	}
	name := t.Elem().Name()
	if name == "" {
		panic("fun: BindService requires a named type")
	}

	boxWired(service, f)

	serviceGuards := make([]*any, 0, len(guardList))
	for _, guard := range guardList {
		checkGuard(guard)
		serviceGuards = append(serviceGuards, serviceGuardWired(guard, f))
	}
	f.serviceGuards[name] = serviceGuards

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
func (f *Fun) BindGuard(guard Guard) {
	checkGuard(guard)
	f.guards = append(f.guards, serviceGuardWired(guard, f))
}

// callGuard 按 全局 → 服务级 顺序执行 Guard
func (f *Fun) callGuard(c *Ctx, serviceName string) {
	for _, g := range f.guards {
		(*g).(Guard).Guard(*c)
	}
	for _, g := range f.serviceGuards[serviceName] {
		(*g).(Guard).Guard(*c)
	}
}

func (f *Fun) Start(port uint16) {
	addr := fmt.Sprintf(":%d", port)
	err := fasthttp.ListenAndServe(addr, f.handle)
	if err != nil {
		panic(err.Error())
	}
}
