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
// 0 success; 1 framework/client protocol error; 2 business error; 4 external request error; 5 external timeout

export type RequestOptions = {
  signal?: AbortSignal;
  state?: Record<string, string>;
};

export type StreamOptions = {
  signal?: AbortSignal;
  state?: Record<string, string>;
};

export type RequestInterceptor = (
  serviceName: string,
  methodName: string,
  state: Record<string, string>,
  dto?: any
) => Promise<void> | void;

export type ResponseContext = {
  readonly requestState: Readonly<Record<string, string>>;
  readonly response?: Response;
};

export type ResponseInterceptor = (
  serviceName: string,
  methodName: string,
  result: result<any>
) => Promise<result<any> | void> | result<any> | void;

export type ContextResponseInterceptor = (
  serviceName: string,
  methodName: string,
  result: result<any>,
  context: ResponseContext
) => Promise<result<any> | void> | result<any> | void;

function messageOf(error: unknown): string {
  if (error instanceof Error && error.message) return error.message;
  if (typeof error === "string" && error) return error;
  return "unknown error";
}

function failure(status: resultStatus, msg: string): result<any> {
  return { status, msg };
}

function isTimeout(error: unknown, signal?: AbortSignal): boolean {
  const errorName = error !== null && typeof error === "object"
    ? (error as { name?: unknown }).name
    : undefined;
  const reason = signal?.reason;
  const reasonName = reason !== null && typeof reason === "object"
    ? (reason as { name?: unknown }).name
    : undefined;
  return errorName === "TimeoutError" || reasonName === "TimeoutError";
}

function requestFailure(error: unknown, signal: AbortSignal | undefined, stream: boolean): result<any> {
  if (isTimeout(error, signal)) {
    return failure(5, stream ? "Stream timed out" : "Request timed out");
  }
  if (signal?.aborted === true) {
    return failure(4, stream ? "Stream aborted" : "Request aborted");
  }
  const kind = stream ? "External stream request" : "External request";
  return failure(4, ` + "`${kind} failed: ${messageOf(error)}`" + `);
}

function isResult(value: unknown): value is result<any> {
  return value !== null && typeof value === "object" &&
    typeof (value as { status?: unknown }).status === "number";
}

function mediaType(response: Response): string {
  return (response.headers.get("content-type") || "").split(";", 1)[0].trim().toLowerCase();
}

function excerpt(text: string, limit = 180): string {
  const value = text.replace(/\s+/g, " ").trim();
  return value.length <= limit ? value : ` + "`${value.slice(0, limit)}...`" + `;
}

function externalFailure(response: Response, detail?: string): result<any> {
  const timeout = response.status === 408 || response.status === 504;
  const statusText = response.statusText || (timeout ? "timeout" : "request failed");
  const suffix = detail ? ` + "`: ${detail}`" + ` : "";
  return failure(timeout ? 5 : 4, ` + "`HTTP ${response.status} ${statusText}${suffix}`" + `);
}

function responseReadFailure(
  error: unknown,
  response: Response,
  signal: AbortSignal | undefined,
  stream: boolean
): result<any> {
  if (isTimeout(error, signal)) {
    return failure(5, stream ? "Stream timed out" : "Request timed out");
  }
  if (signal?.aborted === true) {
    return failure(4, stream ? "Stream aborted" : "Request aborted");
  }
  const kind = stream ? "Stream" : "Response body";
  return response.ok
    ? failure(4, ` + "`${kind} failed: ${messageOf(error)}`" + `)
    : externalFailure(response, ` + "`response body failed: ${messageOf(error)}`" + `);
}

function parseResult(response: Response, text: string): result<any> {
  const body = text.trim();
  if (!body) {
    return response.ok
      ? failure(1, "Empty response body")
      : externalFailure(response);
  }

  let value: unknown;
  try {
    value = JSON.parse(body);
  } catch {
    if (!response.ok) return externalFailure(response, excerpt(body));
    const type = mediaType(response);
    if (type === "text/html" || /^\s*(?:<!doctype\s+html|<html\b)/i.test(body)) {
      return failure(1, ` + "`Unexpected HTML response: ${excerpt(body)}`" + `);
    }
    return failure(1, ` + "`Invalid JSON response: ${excerpt(body)}`" + `);
  }
  if (!isResult(value)) {
    return response.ok
      ? failure(1, "Invalid fun response")
      : externalFailure(response, "invalid fun response");
  }
  return value;
}

export class Client {
  private url: string;
  private state: Record<string, string> = {};
  private requestInterceptors: RequestInterceptor[] = [];
  private responseInterceptors: ContextResponseInterceptor[] = [];

  constructor(url: string) {
    this.url = url.replace(/\/+$/, "");
  }

  setState(state: Record<string, string>) {
    this.state = state;
  }

  addRequestInterceptor(interceptor: RequestInterceptor) {
    this.requestInterceptors.push(interceptor);
  }

  addResponseInterceptor(interceptor: ResponseInterceptor): void;
  addResponseInterceptor(interceptor: ContextResponseInterceptor): void;
  addResponseInterceptor(interceptor: ResponseInterceptor | ContextResponseInterceptor) {
    this.responseInterceptors.push(interceptor as ContextResponseInterceptor);
  }

  private async interceptResponse(
    serviceName: string,
    methodName: string,
    initial: result<any>,
    requestState: Readonly<Record<string, string>>,
    response?: Response
  ): Promise<result<any>> {
    let current = initial;
    const context: ResponseContext = Object.freeze(
      response === undefined ? { requestState } : { requestState, response }
    );
    for (const interceptor of this.responseInterceptors) {
      try {
        const replaced = await interceptor(serviceName, methodName, current, context);
        if (replaced) current = replaced;
      } catch (error) {
        current = failure(1, ` + "`Response interceptor failed: ${messageOf(error)}`" + `);
      }
    }
    return current;
  }

  private async interceptRequest(
    serviceName: string,
    methodName: string,
    state: Record<string, string>,
    dto: any
  ): Promise<void> {
    for (const interceptor of this.requestInterceptors) {
      await interceptor(serviceName, methodName, state, dto);
    }
  }

  private snapshotState(state: Record<string, string>): Readonly<Record<string, string>> {
    return Object.freeze({ ...state });
  }

  async request<T>(
    serviceName: string,
    methodName: string,
    dto?: any,
    options?: RequestOptions
  ): Promise<result<T>> {
    let state: Record<string, string>;
    try {
      state = { ...this.state, ...options?.state };
    } catch (error) {
      return await this.interceptResponse(
        serviceName,
        methodName,
        failure(1, ` + "`Could not prepare request state: ${messageOf(error)}`" + `),
        Object.freeze({})
      ) as result<T>;
    }
    try {
      await this.interceptRequest(serviceName, methodName, state, dto);
    } catch (error) {
      let requestState: Readonly<Record<string, string>>;
      try {
        requestState = this.snapshotState(state);
      } catch (snapshotError) {
        return await this.interceptResponse(
          serviceName,
          methodName,
          failure(1, ` + "`Could not snapshot request state: ${messageOf(snapshotError)}`" + `),
          Object.freeze({})
        ) as result<T>;
      }
      return await this.interceptResponse(
        serviceName,
        methodName,
        failure(1, ` + "`Request interceptor failed: ${messageOf(error)}`" + `),
        requestState
      ) as result<T>;
    }
    let requestState: Readonly<Record<string, string>>;
    try {
      requestState = this.snapshotState(state);
    } catch (error) {
      return await this.interceptResponse(
        serviceName,
        methodName,
        failure(1, ` + "`Could not snapshot request state: ${messageOf(error)}`" + `),
        Object.freeze({})
      ) as result<T>;
    }

    let body: string;
    try {
      const serialized = JSON.stringify({
        serviceName,
        methodName,
        data: dto,
        ...(Object.keys(requestState).length ? { state: requestState } : {}),
      });
      if (serialized === undefined) throw new Error("serialization produced no output");
      body = serialized;
    } catch (error) {
      return await this.interceptResponse(
        serviceName,
        methodName,
        failure(1, ` + "`Could not serialize request: ${messageOf(error)}`" + `),
        requestState
      ) as result<T>;
    }

    let output: result<any>;
    let response: Response | undefined;
    try {
      response = await fetch(` + "`${this.url}/cell`" + `, {
        method: "POST",
        headers: { "Content-Type": "application/json" },
        body,
        signal: options?.signal,
      });
      try {
        output = parseResult(response, await response.text());
      } catch (error) {
        output = responseReadFailure(error, response, options?.signal, false);
      }
    } catch (error) {
      output = requestFailure(error, options?.signal, false);
    }
    return await this.interceptResponse(serviceName, methodName, output, requestState, response) as result<T>;
  }

  async stream<T>(
    serviceName: string,
    methodName: string,
    dto: any | undefined,
    onMessage: (data: T) => unknown,
    options?: StreamOptions
  ): Promise<result<void>> {
    let state: Record<string, string>;
    try {
      state = { ...this.state, ...options?.state };
    } catch (error) {
      return await this.interceptResponse(
        serviceName,
        methodName,
        failure(1, ` + "`Could not prepare request state: ${messageOf(error)}`" + `),
        Object.freeze({})
      );
    }
    try {
      await this.interceptRequest(serviceName, methodName, state, dto);
    } catch (error) {
      let requestState: Readonly<Record<string, string>>;
      try {
        requestState = this.snapshotState(state);
      } catch (snapshotError) {
        return await this.interceptResponse(
          serviceName,
          methodName,
          failure(1, ` + "`Could not snapshot request state: ${messageOf(snapshotError)}`" + `),
          Object.freeze({})
        );
      }
      return await this.interceptResponse(
        serviceName,
        methodName,
        failure(1, ` + "`Request interceptor failed: ${messageOf(error)}`" + `),
        requestState
      );
    }
    let requestState: Readonly<Record<string, string>>;
    try {
      requestState = this.snapshotState(state);
    } catch (error) {
      return await this.interceptResponse(
        serviceName,
        methodName,
        failure(1, ` + "`Could not snapshot request state: ${messageOf(error)}`" + `),
        Object.freeze({})
      );
    }

    let body: string;
    try {
      const serialized = JSON.stringify({
        serviceName,
        methodName,
        data: dto,
        ...(Object.keys(requestState).length ? { state: requestState } : {}),
      });
      if (serialized === undefined) throw new Error("serialization produced no output");
      body = serialized;
    } catch (error) {
      return await this.interceptResponse(
        serviceName,
        methodName,
        failure(1, ` + "`Could not serialize request: ${messageOf(error)}`" + `),
        requestState
      );
    }

    let response: Response;
    try {
      response = await fetch(` + "`${this.url}/cell`" + `, {
        method: "POST",
        headers: { "Content-Type": "application/json" },
        body,
        signal: options?.signal,
      });
    } catch (error) {
      return await this.interceptResponse(
        serviceName,
        methodName,
        requestFailure(error, options?.signal, true),
        requestState
      );
    }

    if (!response.ok) {
      let text: string;
      try {
        text = await response.text();
      } catch (error) {
        return await this.interceptResponse(
          serviceName,
          methodName,
          responseReadFailure(error, response, options?.signal, true),
          requestState,
          response
        );
      }
      return await this.interceptResponse(
        serviceName,
        methodName,
        parseResult(response, text),
        requestState,
        response
      );
    }

    if (mediaType(response) !== "application/x-ndjson") {
      let text: string;
      try {
        text = await response.text();
      } catch (error) {
        return await this.interceptResponse(
          serviceName,
          methodName,
          responseReadFailure(error, response, options?.signal, true),
          requestState,
          response
        );
      }
      const rpcResult = parseResult(response, text);
      return await this.interceptResponse(
        serviceName,
        methodName,
        rpcResult.status === 0
          ? failure(1, "Expected application/x-ndjson response")
          : rpcResult,
        requestState,
        response
      );
    }

    if (!response.body) {
      return await this.interceptResponse(
        serviceName,
        methodName,
        { status: 0 },
        requestState,
        response
      );
    }

    let reader: ReadableStreamDefaultReader<Uint8Array>;
    try {
      reader = response.body.getReader();
    } catch (error) {
      return await this.interceptResponse(
        serviceName,
        methodName,
        responseReadFailure(error, response, options?.signal, true),
        requestState,
        response
      );
    }
    const decoder = new TextDecoder("utf-8", { fatal: true });
    let buffer = "";
    let lineNumber = 0;
    let failed: result<any> | undefined;
    let cause: unknown;

    const emitLine = async (line: string) => {
      const payload = line.replace(/\r$/, "").trim();
      if (!payload) return;
      let data: T;
      try {
        data = JSON.parse(payload) as T;
      } catch (error) {
        failed = failure(1, ` + "`Invalid NDJSON at line ${lineNumber}: ${excerpt(payload)}`" + `);
        cause = error;
        return;
      }
      try {
        await onMessage(data);
      } catch (error) {
        failed = failure(1, ` + "`Stream callback failed: ${messageOf(error)}`" + `);
        cause = error;
      }
    };

    try {
      for (;;) {
        let part: ReadableStreamReadResult<Uint8Array>;
        try {
          part = await reader.read();
        } catch (error) {
          cause = error;
          if (isTimeout(error, options?.signal)) {
            failed = failure(5, "Stream timed out");
          } else if (options?.signal?.aborted === true) {
            failed = failure(4, "Stream aborted");
          } else {
            failed = failure(4, ` + "`Stream read failed: ${messageOf(error)}`" + `);
          }
          break;
        }
        if (part.done) break;
        try {
          buffer += decoder.decode(part.value, { stream: true });
        } catch (error) {
          failed = failure(1, ` + "`Invalid UTF-8 stream data: ${messageOf(error)}`" + `);
          cause = error;
          break;
        }
        for (;;) {
          const newline = buffer.indexOf("\n");
          if (newline < 0) break;
          const line = buffer.slice(0, newline);
          buffer = buffer.slice(newline + 1);
          lineNumber++;
          await emitLine(line);
          if (failed) break;
        }
        if (failed) break;
      }

      if (!failed) {
        try {
          buffer += decoder.decode();
        } catch (error) {
          failed = failure(1, ` + "`Invalid UTF-8 stream data: ${messageOf(error)}`" + `);
          cause = error;
        }
      }
      if (!failed && buffer.length > 0) {
        lineNumber++;
        await emitLine(buffer);
      }

    } finally {
      if (failed) {
        try {
          await reader.cancel(cause);
        } catch {
          // The reader may already be closed by the runtime.
        }
      }
      try {
        reader.releaseLock();
      } catch {
        // The reader may already be errored or released.
      }
    }
    return await this.interceptResponse(
      serviceName,
      methodName,
      failed || { status: 0 },
      requestState,
      response
    );
  }
}`
}

func (ctx templateTs) genDefaultServiceTemplate() string {
	return `import { Client } from "./client";
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
	return `import { Client{{if .IsIncludeRequest}}, type result, type RequestOptions{{end}}{{if .IsIncludeStream}}{{if not .IsIncludeRequest}}, type result{{end}}, type StreamOptions{{end}} } from "./client";
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
  {{if .IsStream }}async {{.MethodName}}({{if .DtoText}}{{.DtoText}}, {{end}}onMessage: (data: {{.GenericTypeText}}) => unknown, options?: StreamOptions): Promise<result<void>> {
    return await this.client.stream<{{.GenericTypeText}}>("{{$serviceName}}", "{{.MethodName}}", {{if .DtoText}}dto{{else}}undefined{{end}}, onMessage, options)
  }{{else}}async {{.MethodName}}({{if .DtoText}}{{.DtoText}}, {{end}}options?: RequestOptions): Promise<{{.ReturnValueText}}> {
    return await this.client.request<{{.GenericTypeText}}>("{{$serviceName}}", "{{.MethodName}}", {{if .DtoText}}dto{{else}}undefined{{end}}, options)
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
