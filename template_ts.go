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

export type RequestInterceptor = (
  serviceName: string,
  methodName: string,
  dto: any
) => Promise<void> | void;

export type ResponseInterceptor = (
  serviceName: string,
  methodName: string,
  result: result<any>
) => Promise<void> | void;

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
    for (const i of this.requestInterceptors) {
      await i(serviceName, methodName, dto);
    }
    const res = await fetch(this.url + "/cell", {
      method: "POST",
      headers: { "Content-Type": "application/json" },
      body: JSON.stringify({ serviceName, methodName, data: dto, ...(Object.keys(this.state).length ? { state: this.state } : {}) }),
    });
    const out = (await res.json()) as result<T>;
    const anyResult: result<any> = {
      id: out.id,
      code: out.code,
      data: out.data,
      msg: out.msg,
      status: out.status,
    };
    for (const i of this.responseInterceptors) {
      await i(serviceName, methodName, anyResult);
    }
    return out;
  }

  async stream<T>(
    serviceName: string,
    methodName: string,
    dto: any | undefined,
    onMessage: (data: T) => void
  ): Promise<void> {
    for (const i of this.requestInterceptors) {
      await i(serviceName, methodName, dto);
    }
    const res = await fetch(this.url + "/cell", {
      method: "POST",
      headers: { "Content-Type": "application/json" },
      body: JSON.stringify({ serviceName, methodName, data: dto, ...(Object.keys(this.state).length ? { state: this.state } : {}) }),
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
