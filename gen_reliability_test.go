package fun

import (
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"
)

type AlphaGenDto struct {
	Value string
}

type ZebraGenDto struct {
	Value string
}

type AlphaGenSvc struct{}

func (*AlphaGenSvc) Zebra(dto ZebraGenDto) (AlphaGenDto, error) { return AlphaGenDto{}, nil }
func (*AlphaGenSvc) Alpha(dto AlphaGenDto) (ZebraGenDto, error) { return ZebraGenDto{}, nil }
func (*AlphaGenSvc) Ping() error                                { return nil }

type MixedGenSvc struct{}

func (*MixedGenSvc) Request() (string, error) { return "", nil }
func (*MixedGenSvc) Stream() (*Stream, error) { return &Stream{}, nil }

type ZebraGenSvc struct{}

func (*ZebraGenSvc) Watch() (*Stream, error) { return &Stream{}, nil }

func isolateGeneratorGlobals(t *testing.T) {
	t.Helper()
	oldFun, oldDirectory := fun, directory
	fun = nil
	directory = "./gen"
	t.Cleanup(func() {
		fun = oldFun
		directory = oldDirectory
	})
}

func generatedFiles(t *testing.T, root string) map[string]string {
	t.Helper()
	files := map[string]string{}
	err := filepath.Walk(root, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		if info.IsDir() {
			return nil
		}
		body, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(root, path)
		if err != nil {
			return err
		}
		files[rel] = string(body)
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	return files
}

func TestGeneratedTypeScriptSignaturesAndImports(t *testing.T) {
	isolateGeneratorGlobals(t)
	f := GetFun()
	f.BindService(&ZebraGenSvc{})
	f.BindService(&MixedGenSvc{})
	f.BindService(&AlphaGenSvc{})
	SetOutput(t.TempDir())
	GenCode(GenTs{})

	read := func(name string) string {
		t.Helper()
		body, err := os.ReadFile(filepath.Join(getDirectory(), "ts", name))
		if err != nil {
			t.Fatal(err)
		}
		return string(body)
	}

	alpha := read("alphaGenSvc.ts")
	if first := strings.SplitN(alpha, "\n", 2)[0]; first != `import { Client, type result, type RequestOptions } from "./client";` {
		t.Fatalf("unexpected request-only imports: %s", first)
	}
	for _, want := range []string{
		`async ping(options?: RequestOptions): Promise<result<void>>`,
		`this.client.request<void>("alphaGenSvc", "ping", undefined, options)`,
		`async alpha(dto:alphaGenDto, options?: RequestOptions): Promise<result<zebraGenDto>>`,
		`this.client.request<zebraGenDto>("alphaGenSvc", "alpha", dto, options)`,
	} {
		if !strings.Contains(alpha, want) {
			t.Errorf("alphaGenSvc.ts missing %q:\n%s", want, alpha)
		}
	}
	if strings.Index(alpha, `import type alphaGenDto`) > strings.Index(alpha, `import type zebraGenDto`) {
		t.Fatalf("DTO imports are not sorted:\n%s", alpha)
	}
	if strings.Index(alpha, `async alpha`) > strings.Index(alpha, `async ping`) ||
		strings.Index(alpha, `async ping`) > strings.Index(alpha, `async zebra`) {
		t.Fatalf("methods are not sorted:\n%s", alpha)
	}

	stream := read("zebraGenSvc.ts")
	if first := strings.SplitN(stream, "\n", 2)[0]; first != `import { Client, type result, type StreamOptions } from "./client";` {
		t.Fatalf("unexpected stream-only imports: %s", first)
	}
	for _, want := range []string{
		`async watch(onMessage: (data: any) => unknown, options?: StreamOptions): Promise<result<void>>`,
		`this.client.stream<any>("zebraGenSvc", "watch", undefined, onMessage, options)`,
	} {
		if !strings.Contains(stream, want) {
			t.Errorf("zebraGenSvc.ts missing %q:\n%s", want, stream)
		}
	}

	mixed := read("mixedGenSvc.ts")
	if first := strings.SplitN(mixed, "\n", 2)[0]; first != `import { Client, type result, type RequestOptions, type StreamOptions } from "./client";` {
		t.Fatalf("unexpected mixed imports: %s", first)
	}
}

func TestGeneratedSourcesAreDeterministic(t *testing.T) {
	isolateGeneratorGlobals(t)
	f := GetFun()
	f.BindService(&ZebraGenSvc{})
	f.BindService(&AlphaGenSvc{})
	f.BindService(&MixedGenSvc{})
	root := t.TempDir()
	SetOutput(root)
	GenCode(GenGo{}, GenTs{})
	first := generatedFiles(t, root)
	GenCode(GenGo{}, GenTs{})
	second := generatedFiles(t, root)
	if len(first) != len(second) {
		t.Fatalf("generated file count changed: %d != %d", len(first), len(second))
	}
	for name, body := range first {
		if second[name] != body {
			t.Errorf("generated file changed between runs: %s", name)
		}
	}

	tsFun := first[filepath.Join("ts", "fun.ts")]
	positions := []int{
		strings.Index(tsFun, `import alphaGenSvc`),
		strings.Index(tsFun, `import mixedGenSvc`),
		strings.Index(tsFun, `import zebraGenSvc`),
	}
	if !sort.IntsAreSorted(positions) || positions[0] < 0 {
		t.Fatalf("TypeScript services are not sorted:\n%s", tsFun)
	}
	goFun := first[filepath.Join("go", "fun.go")]
	positions = []int{
		strings.Index(goFun, "AlphaGenSvc *AlphaGenSvc"),
		strings.Index(goFun, "MixedGenSvc *MixedGenSvc"),
		strings.Index(goFun, "ZebraGenSvc *ZebraGenSvc"),
	}
	if !sort.IntsAreSorted(positions) || positions[0] < 0 {
		t.Fatalf("Go services are not sorted:\n%s", goFun)
	}
	goService := first[filepath.Join("go", "alpha_gen_svc.go")]
	positions = []int{
		strings.Index(goService, "func (ctx *AlphaGenSvc) Alpha("),
		strings.Index(goService, "func (ctx *AlphaGenSvc) Ping("),
		strings.Index(goService, "func (ctx *AlphaGenSvc) Zebra("),
	}
	if !sort.IntsAreSorted(positions) || positions[0] < 0 {
		t.Fatalf("Go methods are not sorted:\n%s", goService)
	}
}
