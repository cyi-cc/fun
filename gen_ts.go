package fun

import (
	"reflect"
	"strings"
)

type GenTs struct {
	template templateTs
}

func (ctx GenTs) typeToTemplateType(t reflect.Type) string {
	text := ""
	if t.Kind() == reflect.Ptr {
		text += " | null"
		t = t.Elem()
	}
	switch t.Kind() {
	case reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64,
		reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64:
		if t.Kind() == reflect.Uint8 && (t.Implements(displayEnumType) || t.Implements(enumType)) {
			text = t.Name() + text
		} else {
			text = "number" + text
		}
	case reflect.Bool:
		text = "boolean" + text
	case reflect.String, reflect.Struct:
		text = t.Name() + text
	default:
		text = ctx.typeToTemplateType(t.Elem()) + "[]" + text
	}
	return text
}

func (ctx GenTs) genService(svc *genSvc, serviceContext *genServiceType) {
	var nestedImports []*genImportType

	for _, gm := range svc.methods {
		var returnValueText string
		var dtoText string
		var argsText string
		var genericTypeText string

		// () error：无数据返回，客户端类型为 void
		if gm.sig.NumOut() == 1 && gm.sig.Out(0) == errorType {
			genericTypeText = "void"
			returnValueText = "result<void>"
			if gm.dtoType != nil {
				v := firstLetterToLower(ctx.typeToTemplateType(gm.dtoType))
				if !strings.Contains(v, "[]") && strings.Contains(v, "[") {
					dtoText += "dto:" + getGenericTypeName(v) + parseGenericTypeParams(v)
				} else {
					dtoText += "dto:" + v
				}
				argsText += ",dto"
				nestedImports = append(nestedImports, ctx.genStruct(gm.dtoType))
			}
			serviceContext.GenMethodTypeList = append(serviceContext.GenMethodTypeList, &genMethodType{
				MethodName:      firstLetterToLower(gm.name),
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
				returnValueText = "void"
			} else {
				t := firstLetterToLower(ctx.typeToTemplateType(returnType))
				if !strings.Contains(t, "[]") && strings.Contains(t, "[") {
					genericTypeText = getGenericTypeName(t) + parseGenericTypeParams(t)
				} else {
					genericTypeText = t
				}
				returnValueText = "void"
				nestedImports = ctx.genReturnTypes(returnType, nestedImports)
			}
		} else {
			t := firstLetterToLower(ctx.typeToTemplateType(returnType))
			if !strings.Contains(t, "[]") && strings.Contains(t, "[") {
				returnValueText = getGenericTypeName(t) + parseGenericTypeParams(t)
			} else {
				returnValueText = t
			}
			genericTypeText = returnValueText
			nestedImports = ctx.genReturnTypes(returnType, nestedImports)
			returnValueText = "result<" + returnValueText + ">"
		}

		if gm.dtoType != nil {
			v := firstLetterToLower(ctx.typeToTemplateType(gm.dtoType))
			if !strings.Contains(v, "[]") && strings.Contains(v, "[") {
				dtoText += "dto:" + getGenericTypeName(v) + parseGenericTypeParams(v)
			} else {
				dtoText += "dto:" + v
			}
			argsText += ",dto"
			nestedImports = append(nestedImports, ctx.genStruct(gm.dtoType))
		}

		serviceContext.GenMethodTypeList = append(serviceContext.GenMethodTypeList, &genMethodType{
			MethodName:      firstLetterToLower(gm.name),
			ReturnValueText: returnValueText,
			DtoText:         dtoText,
			ArgsText:        argsText,
			GenericTypeText: firstLetterToLower(genericTypeText),
			IsStream:        gm.isStream,
		})
	}
	serviceContext.GenImport = deduplicateServiceImports(nestedImports)

	genCode(
		ctx.template.genServiceTemplate(),
		firstLetterToLower(svc.name),
		serviceContext,
		ctx.getName(),
	)
}

// genReturnTypes 递归生成返回类型涉及的 struct/enum 导入
func (ctx GenTs) genReturnTypes(returnType reflect.Type, nestedImports []*genImportType) []*genImportType {
	if returnType.Kind() == reflect.Ptr {
		returnType = returnType.Elem()
	}
	if returnType.Kind() == reflect.Struct {
		nestedImports = append(nestedImports, ctx.genStruct(returnType))
	}
	if returnType.Kind() == reflect.Slice {
		fieldType := returnType.Elem()
		if fieldType.Kind() == reflect.Ptr {
			fieldType = fieldType.Elem()
		}
		if fieldType.Kind() == reflect.Struct {
			nestedImports = append(nestedImports, ctx.genStruct(fieldType))
		}
	}
	if returnType.Kind() == reflect.Uint8 && (returnType.Implements(displayEnumType) || returnType.Implements(enumType)) {
		nestedImports = append(nestedImports, ctx.getEnum(returnType))
	}
	return nestedImports
}

func (ctx GenTs) genDefaultService() {
	f := GetFun()
	genContext := genType{GenServiceList: []*genServiceType{}}

	for svcName, methods := range f.serviceGroups() {
		serviceContext := &genServiceType{
			ServiceName:       firstLetterToLower(svcName),
			GenMethodTypeList: []*genMethodType{},
		}
		genContext.GenServiceList = append(genContext.GenServiceList, serviceContext)
		ctx.genService(&genSvc{name: svcName, methods: methods}, serviceContext)
	}
	genCode(ctx.template.genClientTemplate(), "client", nil, ctx.getName())
	genCode(ctx.template.genDefaultServiceTemplate(), "fun", genContext, ctx.getName())
}

func (ctx GenTs) genStruct(t reflect.Type) *genImportType {
	var structTemplate genClassType
	if !strings.Contains(t.String(), "[]") && strings.Contains(t.String(), "[") {
		structTemplate = genClassType{
			Name: firstLetterToLower(getGenericTypeName(t.Name())) + parseGenericTypeParams(t.Name()),
		}
	} else {
		structTemplate = genClassType{
			Name: firstLetterToLower(t.Name()),
		}
	}
	var nestedImports []*genImportType

	for i := 0; i < t.NumField(); i++ {
		field := t.Field(i)
		fieldType := field.Type
		jsType := ctx.typeToTemplateType(fieldType)
		name := field.Name
		if fieldType.Kind() == reflect.Ptr {
			fieldType = fieldType.Elem()
			name += "?"
		}
		if !strings.Contains(jsType, "[]") && strings.Contains(jsType, "[") {
			structTemplate.GenClassFieldType = append(structTemplate.GenClassFieldType, &genClassFieldType{
				Name: firstLetterToLower(name),
				Type: firstLetterToLower(getGenericTypeName(jsType)) + parseGenericTypeParams(jsType),
			})
		} else {
			structTemplate.GenClassFieldType = append(structTemplate.GenClassFieldType, &genClassFieldType{
				Name: firstLetterToLower(name),
				Type: firstLetterToLower(jsType),
			})
		}

		if fieldType.Kind() == reflect.Struct {
			nestedImports = append(nestedImports, ctx.genStruct(fieldType))
		}
		if fieldType.Kind() == reflect.Slice && fieldType.Elem().Kind() == reflect.Struct {
			nestedImports = append(nestedImports, ctx.genStruct(fieldType.Elem()))
		}
		if fieldType.Kind() == reflect.Uint8 && (fieldType.Implements(displayEnumType) || fieldType.Implements(enumType)) {
			nestedImports = append(nestedImports, ctx.getEnum(fieldType))
		}
	}

	structTemplate.GenImport = deduplicateServiceImports(nestedImports)

	genCode(
		ctx.template.genStructTemplate(),
		structTemplate.Name,
		structTemplate,
		ctx.getName(),
	)

	if !strings.Contains(t.String(), "[]") && strings.Contains(t.String(), "[") {
		return &genImportType{Name: structTemplate.Name}
	}
	return &genImportType{Name: firstLetterToLower(t.Name())}
}

func (ctx GenTs) getEnum(t reflect.Type) *genImportType {
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
	enumTemplate.Name = firstLetterToLower(t.Name())

	genCode(
		ctx.template.genEnumTemplate(),
		firstLetterToLower(t.Name()),
		enumTemplate,
		ctx.getName(),
	)
	return &genImportType{Name: firstLetterToLower(t.Name())}
}

func (ctx GenTs) getName() string {
	return "ts"
}
