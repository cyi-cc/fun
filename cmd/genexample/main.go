// genexample 重新生成 example/gen 下的客户端产物（Go + TS）。
// 在仓库根目录执行：go run ./cmd/genexample
package main

import (
	"fmt"
	"io/fs"
	"os"
	"path/filepath"

	"github.com/cyi-cc/fun"
	"github.com/cyi-cc/fun/example/demo"
)

func main() {
	f := fun.New()
	for _, svc := range []any{&demo.OrderSvc{}, &demo.ChatSvc{}} {
		f.BindServiceForGen(svc) // 只登记元信息，不触发依赖装配
	}
	fun.SetOutput("./example/gen")
	fun.GenCode(fun.GenGo{}, fun.GenTs{})

	err := filepath.WalkDir("./example/gen", func(path string, d fs.DirEntry, err error) error {
		if err == nil && !d.IsDir() {
			rel, _ := filepath.Rel("./example/gen", path)
			fmt.Println("生成:", filepath.ToSlash(rel))
		}
		return err
	})
	if err != nil {
		fmt.Fprintln(os.Stderr, "walk output:", err)
		os.Exit(1)
	}
}
