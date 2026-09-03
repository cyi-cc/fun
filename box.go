package fun

import (
	"fmt"
	"reflect"
)

// boxEntry 依赖容器条目：装配完成的单例，或粘性初始化错误。
// 装配失败的类型记录错误后不再重试，也不把半初始化实例暴露给后续装配
type boxEntry struct {
	val reflect.Value
	err error
}

// Wired 创建并注册一个依赖实例；auto 标签字段递归注入依赖，存在 New() 则调用。
// New 支持 () 与 () error 两种签名，返回非 nil error 即装配失败。
// 失败以 error 返回；同类型再次 Wired 返回同一错误（粘性），避免启动期反复重连
func Wired[T any]() (*T, error) {
	t := reflect.TypeFor[T]()
	if t.Kind() != reflect.Struct {
		panic("Fun: " + t.Name() + " It must be a structure")
	}
	if t.Name() == "" {
		panic("Fun: Wired requires a named struct type")
	}
	if isPrivate(t.Name()) {
		panic("Fun:" + t.Name() + " cannot be Private")
	}
	pt := reflect.TypeFor[*T]()
	checkNewSignature(pt, t.Name())
	f := GetFun()
	f.mu.Lock()
	defer f.mu.Unlock()
	if entry, ok := f.boxes.Load(pt); ok {
		e := entry.(boxEntry)
		if e.err != nil {
			return nil, e.err
		}
		return e.val.Interface().(*T), nil
	}
	data := new(T)
	v := reflect.ValueOf(data)
	// 先入容器再装配：循环依赖（A→B→A）靠占位引用解开；
	// 失败时下面覆盖为粘性错误，容器中不留可用半成品
	f.boxes.Store(pt, boxEntry{val: v})
	if err := f.wireStruct(v.Elem()); err != nil {
		f.boxes.Store(pt, boxEntry{err: err})
		return nil, err
	}
	if err := callNewIfPresent(v); err != nil {
		f.boxes.Store(pt, boxEntry{err: err})
		return nil, err
	}
	return data, nil
}

// wireStruct 注入 auto 标签字段；依赖缺失时递归装配（须持有 f.mu）
func (f *Fun) wireStruct(structValue reflect.Value) error {
	t := structValue.Type()
	for i := 0; i < t.NumField(); i++ {
		c := t.Field(i)
		if _, isAuto := newTag(c.Tag).getTag("auto"); !isAuto {
			continue
		}
		if c.Anonymous {
			panic("Fun:" + c.Name + " cannot be Anonymous")
		}
		if entry, loaded := f.boxes.Load(c.Type); loaded {
			e := entry.(boxEntry)
			if e.err != nil {
				return e.err
			}
			structValue.Field(i).Set(e.val)
			continue
		}
		if err := f.autowired(structValue.Field(i)); err != nil {
			return err
		}
	}
	return nil
}

// autowired 递归创建依赖实例并注入 auto 字段（须持有 f.mu）。
// 实例先入容器再装配字段，供循环依赖拿到占位引用；失败时覆盖为粘性错误
func (f *Fun) autowired(fieldValue reflect.Value) error {
	if fieldValue.Kind() != reflect.Ptr || fieldValue.Type().Elem().Kind() != reflect.Struct {
		panic("Fun: auto field " + fieldValue.Type().String() + " must be a pointer to a struct")
	}
	if isPrivate(fieldValue.Type().Elem().Name()) {
		panic("Fun:" + fieldValue.Type().Elem().Name() + " cannot be Private")
	}
	pt := fieldValue.Type()
	checkNewSignature(pt, pt.Elem().Name())
	instance := reflect.New(pt.Elem())
	f.boxes.Store(pt, boxEntry{val: instance})
	fieldValue.Set(instance)
	if err := f.wireStruct(instance.Elem()); err != nil {
		f.boxes.Store(pt, boxEntry{err: err})
		return err
	}
	if err := callNewIfPresent(instance); err != nil {
		f.boxes.Store(pt, boxEntry{err: err})
		return err
	}
	return nil
}

// checkNewSignature 校验 New 方法签名：无参数，返回 () 或 (error)。
// 在指针类型上查找，兼容值接收器与指针接收器两种定义
func checkNewSignature(pt reflect.Type, name string) {
	m, found := pt.MethodByName("New")
	if !found {
		return
	}
	mt := m.Type
	if mt.NumIn() != 1 { // 仅接收者
		panic("Fun:" + name + " New method must have no parameters")
	}
	if mt.NumOut() == 0 {
		return
	}
	if mt.NumOut() == 1 && mt.Out(0) == errorType {
		return
	}
	panic("Fun:" + name + " New method must return nothing or error")
}

// callNewIfPresent 调用指针上的 New()（若存在），支持 () 与 () error 两种签名
func callNewIfPresent(ptr reflect.Value) error {
	m := ptr.MethodByName("New")
	if !m.IsValid() {
		return nil
	}
	out := m.Call(nil)
	if len(out) == 1 {
		if err, ok := out[0].Interface().(error); ok {
			return err
		}
	}
	return nil
}

// boxWired 注册期预初始化服务结构体字段中的 Box 依赖（须持有 f.mu）。
// 字段对应类型装配失败时向上返回 error
func boxWired(service any, f *Fun) error {
	svcType := reflect.TypeOf(service).Elem()
	serviceInstance := reflect.New(svcType).Elem()
	for i := 0; i < serviceInstance.NumField(); i++ {
		field := serviceInstance.Field(i)
		if field.Type() == ctxType {
			continue
		}
		if field.Type().Kind() == reflect.Ptr && field.Type().Elem().Kind() == reflect.Struct {
			if entry, isWired := f.boxes.Load(field.Type()); isWired {
				if e := entry.(boxEntry); e.err != nil {
					return e.err
				}
				continue
			}
			if err := f.autowired(field); err != nil {
				return fmt.Errorf("fun: wire %s.%s: %w", svcType.Name(), svcType.Field(i).Name, err)
			}
		}
	}
	return nil
}

// serviceWired 每请求把 Ctx 与 Box 依赖注入到新创建的服务实例（只读容器，无锁）
func (f *Fun) serviceWired(serviceInstance reflect.Value, ctx *Ctx) {
	for i := 0; i < serviceInstance.NumField(); i++ {
		field := serviceInstance.Field(i)
		if !field.CanSet() {
			continue
		}
		if field.Type() == ctxType {
			field.Set(reflect.ValueOf(*ctx))
		} else if dependency, ok := f.boxes.Load(field.Type()); ok {
			if e := dependency.(boxEntry); e.err == nil {
				field.Set(e.val)
			}
		}
	}
}

// checkGuard 校验 Guard 类型：必须是指向结构体的指针
func checkGuard(guard Guard) {
	t := reflect.TypeOf(guard)
	if t.Kind() != reflect.Ptr || t.Elem().Kind() != reflect.Struct {
		panic("Fun: guard must be a pointer to a struct")
	}
	if isPrivate(t.Elem().Name()) {
		panic("Fun:" + t.Elem().Name() + " cannot be Private")
	}
}

// serviceGuardWired 创建 Guard 实例并注入 Box 依赖，返回 guard 引用（须持有 f.mu）
func serviceGuardWired(guard Guard, f *Fun) (*any, error) {
	t := reflect.TypeOf(guard).Elem()
	guardInstance := reflect.New(t).Elem()
	for i := 0; i < guardInstance.NumField(); i++ {
		field := guardInstance.Field(i)
		if !field.CanSet() {
			continue
		}
		if field.Type() == ctxType {
			continue
		}
		if entry, ok := f.boxes.Load(field.Type()); ok {
			e := entry.(boxEntry)
			if e.err != nil {
				return nil, e.err
			}
			field.Set(e.val)
		} else if field.Kind() == reflect.Ptr && field.Type().Elem().Kind() == reflect.Struct {
			if err := f.autowired(field); err != nil {
				return nil, err
			}
		}
	}
	g := guardInstance.Addr().Interface()
	return &g, nil
}
