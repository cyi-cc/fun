package fun

import (
	"reflect"
	"strings"
)

type GenGo struct {
	template templateGo
}

func (ctx GenGo) typeToTemplateType(t reflect.Type) string {
	text := ""
	if t.Kind() == reflect.Ptr {
		text += "*"
		t = t.Elem()
	}
	switch t.Kind() {
	case reflect.Slice:
		text += "[]" + ctx.typeToTemplateType(t.Elem())
	default:
		text += t.Name()
	}
	return text
}

func (ctx GenGo) genService(svc *genSvc, serviceContext *genServiceType) {
	for _, gm := range svc.methods {
		var returnValueText string
		var dtoText string
		var argsText string
		var genericTypeText string

		// () error：无数据返回，客户端类型为 Void
		if gm.sig.NumOut() == 1 && gm.sig.Out(0) == errorType {
			genericTypeText = "Void"
			returnValueText = "Result[Void]"
			if gm.dtoType != nil {
				v := ctx.typeToTemplateType(gm.dtoType)
				if !strings.Contains(v, "[]") && strings.Contains(v, "[") {
					dtoText += "dto " + getGenericTypeName(v) + parseGenericTypeParams(v)
				} else {
					dtoText += "dto " + v
				}
				argsText += ",dto"
				ctx.genStruct(gm.dtoType)
			}
			serviceContext.GenMethodTypeList = append(serviceContext.GenMethodTypeList, &genMethodType{
				MethodName:      gm.name,
				ReturnValueText: returnValueText,
				DtoText:         dtoText,
				ArgsText:        argsText,
				GenericTypeText: genericTypeText,
			})
			continue
		}

		returnType := gm.sig.Out(0)
		if gm.isStream {
			serviceContext.IsIncludeStream = true
			if returnType == streamType {
				genericTypeText = "any"
				returnValueText = "Void"
			} else {
				t := ctx.typeToTemplateType(returnType)
				if !strings.Contains(t, "[]") && strings.Contains(t, "[") {
					genericTypeText = getGenericTypeName(t) + parseGenericTypeParams(t)
				} else {
					genericTypeText = t
				}
				returnValueText = genericTypeText
				ctx.genReturnTypes(returnType)
			}
		} else {
			t := ctx.typeToTemplateType(returnType)
			if !strings.Contains(t, "[]") && strings.Contains(t, "[") {
				returnValueText = getGenericTypeName(t) + parseGenericTypeParams(t)
			} else {
				returnValueText = t
			}
			genericTypeText = returnValueText
			ctx.genReturnTypes(returnType)
			returnValueText = "Result[" + returnValueText + "]"
		}

		if gm.dtoType != nil {
			v := ctx.typeToTemplateType(gm.dtoType)
			if !strings.Contains(v, "[]") && strings.Contains(v, "[") {
				dtoText += "dto " + getGenericTypeName(v) + parseGenericTypeParams(v)
			} else {
				dtoText += "dto " + v
			}
			argsText += ",dto"
			ctx.genStruct(gm.dtoType)
		}

		serviceContext.GenMethodTypeList = append(serviceContext.GenMethodTypeList, &genMethodType{
			MethodName:      gm.name,
			ReturnValueText: returnValueText,
			DtoText:         dtoText,
			ArgsText:        argsText,
			GenericTypeText: genericTypeText,
			IsStream:        gm.isStream,
		})
	}
	genCode(ctx.template.genServiceTemplate(), camelToSnake(svc.name), serviceContext, ctx.getName())
}

// genReturnTypes 递归生成返回类型涉及的 struct/enum 定义
func (ctx GenGo) genReturnTypes(returnType reflect.Type) {
	if returnType.Kind() == reflect.Ptr {
		returnType = returnType.Elem()
	}
	if returnType.Kind() == reflect.Struct {
		ctx.genStruct(returnType)
	}
	if returnType.Kind() == reflect.Slice {
		fieldType := returnType.Elem()
		if fieldType.Kind() == reflect.Ptr {
			fieldType = fieldType.Elem()
		}
		if fieldType.Kind() == reflect.Struct {
			ctx.genStruct(fieldType)
		}
	}
	if returnType.Kind() == reflect.Uint8 && (returnType.Implements(displayEnumType) || returnType.Implements(enumType)) {
		ctx.getEnum(returnType)
	}
}

func (ctx GenGo) genDefaultService() {
	f := GetFun()
	genContext := genType{GenServiceList: []*genServiceType{}}

	for _, svc := range f.serviceGroups() {
		serviceContext := &genServiceType{
			ServiceName:       svc.name,
			GenMethodTypeList: []*genMethodType{},
		}
		genContext.GenServiceList = append(genContext.GenServiceList, serviceContext)
		ctx.genService(svc, serviceContext)
	}
	genCode(ctx.template.genDefaultServiceTemplate(), "fun", genContext, ctx.getName())
}

func (ctx GenGo) genStruct(t reflect.Type) *genImportType {
	var structTemplate genClassType
	if !strings.Contains(t.String(), "[]") && strings.Contains(t.String(), "[") {
		structTemplate = genClassType{
			Name: getGenericTypeName(t.Name()) + parseGenericTypeParams(t.Name()),
		}
	} else {
		structTemplate = genClassType{
			Name: t.Name(),
		}
	}

	for i := 0; i < t.NumField(); i++ {
		field := t.Field(i)
		fieldType := field.Type
		jsType := ctx.typeToTemplateType(fieldType)
		name := field.Name
		tag := "`json:\"" + firstLetterToLower(name) + "\"`"
		if !strings.Contains(jsType, "[]") && strings.Contains(jsType, "[") {
			structTemplate.GenClassFieldType = append(structTemplate.GenClassFieldType, &genClassFieldType{
				Name: name,
				Type: getGenericTypeName(jsType) + parseGenericTypeParams(jsType),
				Tag:  tag,
			})
		} else {
			structTemplate.GenClassFieldType = append(structTemplate.GenClassFieldType, &genClassFieldType{
				Name: name,
				Type: jsType,
				Tag:  tag,
			})
		}

		if fieldType.Kind() == reflect.Struct {
			ctx.genStruct(fieldType)
		}
		if fieldType.Kind() == reflect.Slice && fieldType.Elem().Kind() == reflect.Struct {
			ctx.genStruct(fieldType.Elem())
		}
		if fieldType.Kind() == reflect.Uint8 && (fieldType.Implements(displayEnumType) || fieldType.Implements(enumType)) {
			ctx.getEnum(fieldType)
		}
	}

	genCode(
		ctx.template.genStructTemplate(),
		camelToSnake(structTemplate.Name),
		structTemplate,
		ctx.getName(),
	)
	return &genImportType{}
}

func (ctx GenGo) getEnum(t reflect.Type) *genImportType {
	var enumTemplate genEnumType
	statusValue := reflect.New(t).Elem()
	if t.Implements(displayEnumType) {
		enumValue := statusValue.Interface().(displayEnum)
		enumTemplate.Names = enumValue.Names()
		enumTemplate.DisplayNames = enumValue.DisplayNames()
	} else {
		enumValue := statusValue.Interface().(enum)
		enumTemplate.Names = enumValue.Names()
	}
	enumTemplate.Name = t.Name()

	genCode(
		ctx.template.genEnumTemplate(),
		camelToSnake(t.Name()),
		enumTemplate,
		ctx.getName(),
	)
	return &genImportType{}
}

func (ctx GenGo) getName() string {
	return "go"
}
