package fun

import (
	"errors"
	"fmt"
	"reflect"
	"strings"
	"unicode"
)

func isPrivate(value string) bool {
	return !unicode.IsUpper([]rune(value)[0])
}

// checkType 注册期递归校验类型是否受支持：
// int/uint/string/bool/struct/slice/enum；不支持匿名结构体、私有类型、空结构体；
// 枚举要求 Names/DisplayNames 长度一致
func checkType(t reflect.Type) {
	if t.Kind() == reflect.Ptr {
		t = t.Elem()
	}
	if strings.Contains(t.String(), "{}") {
		panic(fmt.Sprintf("fun: %s generic types containing 'any' or interface{} are not supported", t.Name()))
	}
	switch t.Kind() {
	case reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64,
		reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64,
		reflect.String, reflect.Bool:
		if t.Kind() == reflect.Uint8 && (t.Implements(displayEnumType) || t.Implements(enumType)) && isPrivate(t.Name()) {
			panic("fun:" + t.Name() + " cannot be Private")
		}
		if t.Kind() == reflect.Uint8 && t.Implements(displayEnumType) {
			enumValue := reflect.New(t).Elem().Interface().(displayEnum)
			if len(enumValue.DisplayNames()) != len(enumValue.Names()) {
				panic("fun: " + t.Name() + " enum names and display names must be the same length")
			}
		}
	case reflect.Struct:
		if t.NumField() == 0 {
			panic("fun: " + t.Name() + " must have at least one field")
		}
		for i := 0; i < t.NumField(); i++ {
			f := t.Field(i)
			if isPrivate(f.Name) {
				panic("fun:" + f.Name + " cannot be Private")
			}
			checkType(f.Type)
		}
	case reflect.Slice:
		checkType(t.Elem())
	default:
		panic("fun:Unsupported types " + t.Name())
	}
}

// checkDto 运行时校验请求数据：
// 非指针字段必须出现在请求中且非 nil；嵌套 struct/slice 递归；枚举值必须在范围内
func checkDto(dtoType reflect.Type, dtoMap any, methodName string) error {
	t := dtoType
	if t.Kind() == reflect.Ptr {
		t = t.Elem()
	}
	switch t.Kind() {
	case reflect.Struct:
		obj, ok := dtoMap.(map[string]any)
		if !ok {
			return callError(fmt.Errorf("fun: method %s DTO %s must be an object", methodName, t.Name()))
		}
		for i := 0; i < t.NumField(); i++ {
			f := t.Field(i)
			value, ok := obj[firstLetterToLower(f.Name)]
			if !ok {
				// 兼容按原始字段名（首字母大写）传参的客户端
				value, ok = obj[f.Name]
			}
			// 非指针且非切片字段必须存在且非 null；切片字段允许 null（反序列化为 nil slice）
			if f.Type.Kind() != reflect.Ptr && f.Type.Kind() != reflect.Slice && (!ok || value == nil) {
				return callError(fmt.Errorf("fun: %s Dto must be a pointer or have a corresponding field in the map", f.Name))
			}
			ft := f.Type
			if ft.Kind() == reflect.Ptr {
				ft = ft.Elem()
			}
			if (ft.Kind() == reflect.Struct || ft.Kind() == reflect.Slice) && value != nil {
				if err := checkDto(ft, value, methodName); err != nil {
					return err
				}
			}
			if ft.Kind() == reflect.Uint8 && value != nil && (ft.Implements(displayEnumType) || ft.Implements(enumType)) {
				if err := checkEnumValue(ft, value, f.Name); err != nil {
					return err
				}
			}
		}
	case reflect.Slice:
		list, ok := dtoMap.([]any)
		if !ok {
			return callError(fmt.Errorf("fun: Dto must be an array"))
		}
		for _, value := range list {
			et0 := t.Elem()
			et := et0
			if et.Kind() == reflect.Ptr {
				et = et.Elem()
			}
			// 指针元素允许 null，值元素必须非空
			if et0.Kind() != reflect.Ptr && value == nil {
				return callError(fmt.Errorf("fun:%s Dto must be a pointer or have a corresponding field in the map", et0.Name()))
			}
			if (et.Kind() == reflect.Struct || et.Kind() == reflect.Slice) && value != nil {
				if err := checkDto(et, value, methodName); err != nil {
					return err
				}
			}
			if et.Kind() == reflect.Uint8 && value != nil && (et.Implements(displayEnumType) || et.Implements(enumType)) {
				if err := checkEnumValue(et, value, et.Name()); err != nil {
					return err
				}
			}
		}
	}
	return nil
}

// checkEnumValue 运行时校验枚举值是否在范围内
func checkEnumValue(t reflect.Type, value any, name string) error {
	var max uint8
	enumValue := reflect.New(t).Elem()
	if t.Implements(displayEnumType) {
		max = uint8(len(enumValue.Interface().(displayEnum).Names()))
	} else {
		max = uint8(len(enumValue.Interface().(enum).Names()))
	}
	var num uint8
	switch v := value.(type) {
	case float64:
		num = uint8(v)
	case float32:
		num = uint8(v)
	case uint8:
		num = v
	case uint16:
		num = uint8(v)
	case uint32:
		num = uint8(v)
	case uint64:
		num = uint8(v)
	case int:
		num = uint8(v)
	case int8:
		num = uint8(v)
	case int16:
		num = uint8(v)
	case int32:
		num = uint8(v)
	case int64:
		num = uint8(v)
	default:
		return callError(errors.New("Fun:" + name + " Dto enum value type is not supported"))
	}
	if num >= max {
		return callError(errors.New("Fun:" + name + " Dto value out of range"))
	}
	return nil
}
