package fun

import (
	"bytes"
	"os"
	"path/filepath"
	"reflect"
	"regexp"
	"strings"
	"text/template"
)

type Gen interface {
	typeToTemplateType(t reflect.Type) string
	genService(svc *genSvc, serviceContext *genServiceType)
	genDefaultService()
	genStruct(t reflect.Type) *genImportType
	getEnum(t reflect.Type) *genImportType
	getName() string
}

// genSvc 生成器视图下的服务
type genSvc struct {
	name    string
	methods []*genMethod
}

// genMethod 生成器视图下的方法
type genMethod struct {
	name     string
	sig      reflect.Type // 方法签名类型（不含接收者），Out(0) 为返回类型
	dtoType  reflect.Type
	isStream bool
}

// serviceGroups 按服务名分组已注册方法
func (f *Fun) serviceGroups() map[string][]*genMethod {
	groups := map[string][]*genMethod{}
	for key, m := range f.methods {
		parts := strings.SplitN(key, ".", 2)
		svc, name := parts[0], parts[1]
		sig := reflect.New(m.serviceType).Method(m.methodIndex).Type()
		groups[svc] = append(groups[svc], &genMethod{
			name:     name,
			sig:      sig,
			dtoType:  m.dtoType,
			isStream: m.isStream,
		})
	}
	return groups
}

type genType struct {
	GenServiceList []*genServiceType
}

type genMethodType struct {
	MethodName      string
	ReturnValueText string
	DtoText         string
	ArgsText        string
	GenericTypeText string
	IsProxy         bool
	IsStream        bool
}

type genEnumType struct {
	Names        []string
	DisplayNames []string
	Name         string
}

type genImportType struct {
	Name string
}

type genServiceType struct {
	ServiceName       string
	GenMethodTypeList []*genMethodType
	GenImport         []*genImportType
	IsIncludeProxy    bool
	IsIncludeStream   bool
}

type genClassType struct {
	Name              string
	GenImport         []*genImportType
	GenClassFieldType []*genClassFieldType
}

type genClassFieldType struct {
	Name string
	Type string
	Tag  string
}

func deduplicateServiceImports(imports []*genImportType) []*genImportType {
	seen := make(map[string]bool)
	var result []*genImportType
	for _, imp := range imports {
		if !seen[imp.Name] {
			seen[imp.Name] = true
			result = append(result, imp)
		}
	}
	return result
}

func parseGenericTypeParams(typeName string) string {
	start := strings.Index(typeName, "[")
	end := strings.LastIndex(typeName, "]")
	paramsStr := typeName[start+1 : end]
	params := strings.Split(paramsStr, ",")
	for i, param := range params {
		LL := strings.Split(strings.TrimSpace(param), ".")
		params[i] = firstLetterToUpper(LL[len(LL)-1])
	}
	return strings.Join(params, "")
}

func getGenericTypeName(typeName string) string {
	start := strings.Index(typeName, "[")
	return typeName[0:start]
}

func genCode(templateContent string, outputFileName string, templateData any, languageName string) {
	tmpl, err := template.New(languageName).Parse(templateContent)
	if err != nil {
		panic(err.Error())
	}
	var buf bytes.Buffer
	err = tmpl.Execute(&buf, templateData)
	if err != nil {
		panic(err.Error())
	}
	code := buf.Bytes()
	fullPath := filepath.Join(getDirectory(), languageName)

	_, err = os.Stat(fullPath)
	if os.IsNotExist(err) {
		err = os.MkdirAll(fullPath, os.ModePerm)
		if err != nil {
			panic(err.Error())
		}
	}
	err = os.WriteFile(filepath.Join(fullPath, outputFileName+"."+languageName), code, 0644)
	if err != nil {
		panic(err.Error())
	}
}

// GenCode 执行代码生成：清空输出目录后按顺序运行每个生成器
func GenCode(genList ...Gen) {
	if err := os.RemoveAll(getDirectory()); err != nil && !os.IsNotExist(err) {
		panic(err.Error())
	}
	for _, gen := range genList {
		gen.genDefaultService()
	}
}

var directory = "./gen"

func SetOutput(path string) {
	directory = path
}

func getDirectory() string {
	return directory
}

func camelToSnake(s string) string {
	re := regexp.MustCompile(`([a-z0-9])([A-Z])`)
	snake := re.ReplaceAllString(s, `${1}_${2}`)
	return strings.ToLower(snake)
}
