package fun

import (
	"reflect"
)

// Wired 创建并注册一个依赖实例；auto 标签字段递归注入依赖；存在 New() 则调用
func Wired[T any]() *T {
	t := reflect.TypeFor[T]()
	data := new(T)
	if t.Kind() != reflect.Struct {
		panic("Fun: " + t.Name() + " It must be a structure")
	}
	if isPrivate(t.Name()) {
		panic("Fun:" + t.Name() + " cannot be Private")
	}
	if newMethod, found := t.MethodByName("New"); found {
		if newMethod.Type.NumIn() != 1 || newMethod.Type.NumOut() != 0 {
			panic("Fun:" + t.Name() + " New method must have no parameters and no return values")
		}
	}
	f := GetFun()
	if box, isWired := f.boxes.Load(reflect.TypeFor[*T]()); isWired {
		return box.(reflect.Value).Interface().(*T)
	}
	v := reflect.ValueOf(data)
	f.boxes.Store(reflect.TypeFor[*T](), v)
	boxList := map[reflect.Type]bool{}
	for i := 0; i < t.NumField(); i++ {
		c := t.Field(i)
		fieldTag := newTag(c.Tag)
		if _, isAuto := fieldTag.getTag("auto"); isAuto {
			if dependency, loaded := f.boxes.Load(c.Type); loaded {
				v.Elem().Field(i).Set(dependency.(reflect.Value))
			} else {
				checkBox(c, boxList)
				f.autowired(v.Elem().Field(i))
			}
		}
	}
	newMethod := v.MethodByName("New")
	if newMethod.IsValid() {
		newMethod.Call(nil)
	}
	return data
}

// autowired 递归创建依赖实例并注入 auto 标签字段
func (f *Fun) autowired(fieldValue reflect.Value) {
	instance := reflect.New(fieldValue.Type().Elem())
	f.boxes.Store(fieldValue.Type(), instance)
	fieldValue.Set(instance)
	structValue := instance.Elem()
	for i := 0; i < structValue.NumField(); i++ {
		structField := structValue.Type().Field(i)
		fieldTag := newTag(structField.Tag)
		if _, isAuto := fieldTag.getTag("auto"); isAuto {
			if dependency, loaded := f.boxes.Load(structField.Type); loaded {
				structValue.Field(i).Set(dependency.(reflect.Value))
			} else {
				f.autowired(structValue.Field(i))
			}
		}
	}
	newMethod := instance.MethodByName("New")
	if newMethod.IsValid() {
		newMethod.Call(nil)
	}
}

// checkBox 校验 auto 注入字段：必须是指针+struct、非匿名、非私有；New() 必须无参无返回值
func checkBox(s reflect.StructField, boxList map[reflect.Type]bool) {
	if _, ok := boxList[s.Type]; ok {
		return
	}
	boxList[s.Type] = true
	if s.Anonymous {
		panic("Fun:" + s.Name + " cannot be Anonymous")
	}
	if s.Type.Kind() != reflect.Ptr || s.Type.Elem().Kind() != reflect.Struct {
		panic("Fun:" + s.Name + " Must be a pointer and a struct")
	}
	if isPrivate(s.Name) {
		panic("Fun:" + s.Name + " cannot be Private")
	}
	if newMethod, found := s.Type.MethodByName("New"); found {
		if newMethod.Type.NumIn() != 1 || newMethod.Type.NumOut() != 0 {
			panic("Fun:" + s.Name + " New method must have no parameters and no return values")
		}
	}
	for i := 0; i < s.Type.Elem().NumField(); i++ {
		f := s.Type.Elem().Field(i)
		fieldTag := newTag(f.Tag)
		if _, isAuto := fieldTag.getTag("auto"); isAuto {
			checkBox(f, boxList)
		}
	}
}

// boxWired 注册期预初始化服务结构体字段中的 Box 依赖
func boxWired(service any, f *Fun) {
	serviceInstance := reflect.New(reflect.TypeOf(service).Elem()).Elem()
	for i := 0; i < serviceInstance.NumField(); i++ {
		field := serviceInstance.Field(i)
		if field.Type() == ctxType {
			continue
		}
		if field.Type().Kind() == reflect.Ptr && field.Type().Elem().Kind() == reflect.Struct {
			if _, isWired := f.boxes.Load(field.Type()); !isWired {
				f.autowired(field)
			}
		}
	}
}

// serviceWired 每请求把 Ctx 与 Box 依赖注入到新创建的服务实例
func (f *Fun) serviceWired(serviceInstance reflect.Value, ctx *Ctx) {
	for i := 0; i < serviceInstance.NumField(); i++ {
		field := serviceInstance.Field(i)
		if !field.CanSet() {
			continue
		}
		if field.Type() == ctxType {
			field.Set(reflect.ValueOf(*ctx))
		} else if dependency, ok := f.boxes.Load(field.Type()); ok {
			field.Set(dependency.(reflect.Value))
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

// serviceGuardWired 创建 Guard 实例并注入 Box 依赖，返回 guard 引用
func serviceGuardWired(guard Guard, f *Fun) *any {
	t := reflect.TypeOf(guard).Elem()
	guardInstance := reflect.New(t).Elem()
	for i := 0; i < guardInstance.NumField(); i++ {
		field := guardInstance.Field(i)
		if !field.CanSet() {
			continue
		}
		if dependency, ok := f.boxes.Load(field.Type()); ok {
			field.Set(dependency.(reflect.Value))
		} else {
			f.autowired(field)
		}
	}
	g := guardInstance.Addr().Interface()
	return &g
}
