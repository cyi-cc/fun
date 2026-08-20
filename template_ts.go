package fun

type templateTs struct{}

func (ctx templateTs) genClientTemplate() string {
	return `export type result<T> = {
  id?: string;
  code?: number;
  data?: T;
  msg?: string;
  status: number;
};

export type resultStatus = 0 | 1 | 2 | 4 | 5;
// 0 成功；1 框架错误；2 业务错误；4 网络错误；5 超时

export type RequestInterceptor = (
  serviceName: string,
  methodName: string,
  state: Record<string, string>,
  dto?: any
) => Promise<void> | void;

// 返回新 result 将替换原结果继续向下传递（可用于集中换 token / 错误处理）
export type ResponseInterceptor = (
  serviceName: string,
  methodName: string,
  result: result<any>
) => Promise<result<any> | void> | result<any> | void;

export class Client {
  private url: string;
  private state: Record<string, string> = {};
  private requestInterceptors: RequestInterceptor[] = [];
  private responseInterceptors: ResponseInterceptor[] = [];

  constructor(url: string) {
    this.url = url.replace(/\/+$/, "");
  }

  setState(state: Record<string, string>) {
    this.state = state;
  }

  addRequestInterceptor(i: RequestInterceptor) {
    this.requestInterceptors.push(i);
  }

  addResponseInterceptor(i: ResponseInterceptor) {
    this.responseInterceptors.push(i);
  }

  async request<T>(serviceName: string, methodName: string, dto?: any): Promise<result<T>> {
    const state: Record<string, string> = { ...this.state };
    for (const i of this.requestInterceptors) {
      await i(serviceName, methodName, state, dto);
    }
    let out: result<T>;
    try {
      const res = await fetch(this.url + "/cell", {
        method: "POST",
        headers: { "Content-Type": "application/json" },
        body: JSON.stringify({ serviceName, methodName, data: dto, ...(Object.keys(state).length ? { state } : {}) }),
      });
      out = (await res.json()) as result<T>;
    } catch (e: any) {
      out = { status: 4, msg: (e && e.message) || "网络错误" } as result<T>;
    }
    let cur: result<any> = out as result<any>;
    for (const i of this.responseInterceptors) {
      const replaced = await i(serviceName, methodName, cur);
      if (replaced) cur = replaced;
    }
    return cur as result<T>;
  }

  async stream<T>(
    serviceName: string,
    methodName: string,
    dto: any | undefined,
    onMessage: (data: T) => void
  ): Promise<void> {
    const state: Record<string, string> = { ...this.state };
    for (const i of this.requestInterceptors) {
      await i(serviceName, methodName, state, dto);
    }
    const res = await fetch(this.url + "/cell", {
      method: "POST",
      headers: { "Content-Type": "application/json" },
      body: JSON.stringify({ serviceName, methodName, data: dto, ...(Object.keys(state).length ? { state } : {}) }),
    });
    if (!res.ok) return;
    const anyResult: result<any> = { status: 0 };
    for (const i of this.responseInterceptors) {
      await i(serviceName, methodName, anyResult);
    }
    if (!res.body) return;
    const reader = res.body.getReader();
    const decoder = new TextDecoder();
    let buffer = "";
    for (;;) {
      const { done, value } = await reader.read();
      if (done) break;
      buffer += decoder.decode(value, { stream: true });
      const lines = buffer.split("\n");
      buffer = lines.pop() ?? "";
      for (const line of lines) {
        const payload = line.trim();
        if (!payload) continue;
        const data = JSON.parse(payload) as T;
        onMessage(data);
      }
    }
  }
}`
}

func (ctx templateTs) genDefaultServiceTemplate() string {
	return `import { Client, type result } from "./client";
{{- range .GenServiceList}}
import {{.ServiceName}} from "./{{.ServiceName}}";
{{- end}}

export class defaultApi extends Client {
  constructor(url: string) {
    super(url);
  }
  {{- range .GenServiceList}}
  public {{.ServiceName}}: {{.ServiceName}} = new {{.ServiceName}}(this);
  {{- end}}
}

export default class api {
  static create(url: string): defaultApi {
    return new defaultApi(url);
  }
}`
}

func (ctx templateTs) genServiceTemplate() string {
	return `import { Client, type result } from "./client"
{{- range .GenImport}}
import type {{.Name}} from "./{{.Name}}";
{{- end}}

export default class {{.ServiceName}} {
  private client: Client;
  constructor(client: Client) {
    this.client = client;
  }
  {{- $serviceName := .ServiceName }}
  {{- range .GenMethodTypeList}}
  {{if .IsStream }}async {{.MethodName}}({{.DtoText}}{{if .DtoText}},{{end}}onMessage: (data: {{.GenericTypeText}}) => void): Promise<void> {
    return await this.client.stream<{{.GenericTypeText}}>("{{$serviceName}}", "{{.MethodName}}", {{if .DtoText}}dto{{else}}undefined{{end}}, onMessage)
  }{{else}}async {{.MethodName}}({{.DtoText}}): Promise<{{.ReturnValueText}}> {
    return await this.client.request<{{.GenericTypeText}}>("{{$serviceName}}", "{{.MethodName}}"{{.ArgsText}})
  }{{end}}
  {{- end}}
}`
}

func (ctx templateTs) genStructTemplate() string {
	return `{{- range .GenImport}}import type {{.Name}} from "./{{.Name}}";{{"\n"}}{{- end}}export default interface {{.Name}} {
  {{- range .GenClassFieldType}}
  {{.Name}}:{{.Type}}
  {{- end}}
}`
}

func (ctx templateTs) genEnumTemplate() string {
	return `enum {{.Name}} {
{{- range $index, $element := .Names}}
  {{$element}},
{{- end}}
}{{ $enumName := .Name }}
function values(): {{.Name}}[] {
	return [
{{- range $index, $element := .Names}}
        {{$enumName}}.{{$element}},
{{- end}}
	]
}
{{if .DisplayNames}}
function displayNames(): string[] {
	return [
{{- range $index, $element := .DisplayNames}}
        "{{$element}}",
{{- end}}
	]
}
{{end}}
export default {{.Name}}`
}
