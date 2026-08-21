package fun

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestTypeScriptClientBehavior(t *testing.T) {
	node, err := exec.LookPath("node")
	if err != nil {
		t.Skip("node is not installed")
	}
	probe := exec.Command(node, "--experimental-strip-types", "--input-type=module", "-e",
		`if (typeof fetch !== "function" || typeof ReadableStream !== "function") process.exit(1)`)
	if err := probe.Run(); err != nil {
		t.Skip("node does not support TypeScript stripping and web streams")
	}

	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "client.ts"), []byte(templateTs{}.genClientTemplate()), 0o644); err != nil {
		t.Fatal(err)
	}
	const script = `
import assert from "node:assert/strict";
import { Client } from "./client.ts";

const json = (value, status = 200) => new Response(JSON.stringify(value), {
  status,
  headers: { "Content-Type": "application/json" },
});
const ndjson = body => new Response(body, {
  headers: { "Content-Type": "application/x-ndjson; charset=utf-8" },
});
const client = new Client("http://example.test///");
const seen = [];
let cleanupCheck;
client.addResponseInterceptor((_service, _method, result) => {
  if (cleanupCheck) {
    assert.equal(cleanupCheck(), true);
    cleanupCheck = undefined;
  }
  seen.push(result.status);
});
const expectSeen = status => assert.deepEqual(seen.splice(0), [status]);

let fetchInit;
globalThis.fetch = async (url, init) => {
  fetchInit = { url, init };
  return json({ status: 0, data: "ok" });
};
let result = await client.request("Svc", "fetchShape", undefined, { signal: new AbortController().signal });
assert.equal(result.status, 0);
assert.equal(fetchInit.url, "http://example.test/cell");
assert.equal(fetchInit.init.method, "POST");
assert.equal(fetchInit.init.headers["Content-Type"], "application/json");
assert.ok(fetchInit.init.signal);
assert.deepEqual(JSON.parse(fetchInit.init.body), { serviceName: "Svc", methodName: "fetchShape" });
expectSeen(0);

globalThis.fetch = async () => json({ status: 2, code: 4001, msg: "business" }, 503);
result = await client.request("Svc", "business");
assert.deepEqual(result, { status: 2, code: 4001, msg: "business" });
expectSeen(2);

globalThis.fetch = async () => json({ status: 1, msg: "framework" }, 500);
result = await client.request("Svc", "framework");
assert.deepEqual(result, { status: 1, msg: "framework" });
expectSeen(1);

globalThis.fetch = async () => new Response("<html>gateway timeout</html>", { status: 504 });
result = await client.request("Svc", "gatewayTimeout");
assert.equal(result.status, 5);
assert.match(result.msg, /504/);
expectSeen(5);

globalThis.fetch = async () => new Response("request timeout", { status: 408 });
result = await client.request("Svc", "gateway408");
assert.equal(result.status, 5);
expectSeen(5);

globalThis.fetch = async () => json({ status: 5, msg: "server timeout" });
result = await client.request("Svc", "serverTimeout");
assert.equal(result.status, 5);
assert.equal(result.msg, "server timeout");
expectSeen(5);

globalThis.fetch = async () => new Response("bad gateway", { status: 502 });
result = await client.request("Svc", "gateway");
assert.equal(result.status, 4);
expectSeen(4);

globalThis.fetch = async () => new Response("", { status: 200 });
result = await client.request("Svc", "empty");
assert.equal(result.status, 1);
assert.match(result.msg, /Empty/);
expectSeen(1);

globalThis.fetch = async () => new Response("not-json", { status: 200 });
result = await client.request("Svc", "invalidJson");
assert.equal(result.status, 1);
assert.match(result.msg, /Invalid JSON/);
expectSeen(1);

globalThis.fetch = async () => new Response("<html>login</html>", {
  status: 200,
  headers: { "Content-Type": "text/html" },
});
result = await client.request("Svc", "html");
assert.equal(result.status, 1);
assert.match(result.msg, /HTML/);
expectSeen(1);

globalThis.fetch = async () => json({ data: 1 });
result = await client.request("Svc", "invalidResult");
assert.equal(result.status, 1);
expectSeen(1);

globalThis.fetch = async () => { throw new TypeError("DNS failed"); };
result = await client.request("Svc", "network");
assert.equal(result.status, 4);
assert.match(result.msg, /DNS failed/);
expectSeen(4);

globalThis.fetch = async () => new Response(new ReadableStream({
  pull(controller) { controller.error(new Error("body read failed")); },
}));
result = await client.request("Svc", "bodyRead");
assert.equal(result.status, 4);
assert.match(result.msg, /body read failed/);
expectSeen(4);

const manualAbort = new AbortController();
manualAbort.abort();
globalThis.fetch = async () => { throw new DOMException("aborted", "AbortError"); };
result = await client.request("Svc", "abort", undefined, { signal: manualAbort.signal });
assert.equal(result.status, 4);
assert.match(result.msg, /aborted/i);
expectSeen(4);

const timeoutAbort = new AbortController();
timeoutAbort.abort(new DOMException("timed out", "TimeoutError"));
result = await client.request("Svc", "timeoutSignal", undefined, { signal: timeoutAbort.signal });
assert.equal(result.status, 5);
expectSeen(5);

globalThis.fetch = async () => { throw new DOMException("timed out", "TimeoutError"); };
result = await client.request("Svc", "timeoutError");
assert.equal(result.status, 5);
expectSeen(5);

const requestInterceptorClient = new Client("http://example.test");
let requestInterceptorSeen;
requestInterceptorClient.addRequestInterceptor((_service, _method, state, dto) => {
  state.token = "abc";
  requestInterceptorSeen = dto;
});
requestInterceptorClient.addResponseInterceptor((_service, _method, value) => ({ ...value, msg: "intercepted" }));
globalThis.fetch = async (_url, init) => {
  const payload = JSON.parse(init.body);
  assert.deepEqual(payload.state, { token: "abc" });
  return json({ status: 0 });
};
result = await requestInterceptorClient.request("Svc", "interceptors", { value: 1 });
assert.deepEqual(requestInterceptorSeen, { value: 1 });
assert.equal(result.msg, "intercepted");

const failingRequestInterceptor = new Client("http://example.test");
let normalized = [];
failingRequestInterceptor.addRequestInterceptor(() => { throw new Error("request hook"); });
failingRequestInterceptor.addResponseInterceptor((_s, _m, value) => { normalized.push(value.status); });
result = await failingRequestInterceptor.request("Svc", "hook");
assert.equal(result.status, 1);
assert.match(result.msg, /request hook/);
assert.deepEqual(normalized, [1]);

const failingResponseInterceptor = new Client("http://example.test");
normalized = [];
failingResponseInterceptor.addResponseInterceptor(() => { throw new Error("response hook"); });
failingResponseInterceptor.addResponseInterceptor((_s, _m, value) => { normalized.push(value.status); });
globalThis.fetch = async () => json({ status: 2, code: 4003, msg: "original" });
result = await failingResponseInterceptor.request("Svc", "responseHook");
assert.equal(result.status, 1);
assert.match(result.msg, /response hook/);
assert.deepEqual(normalized, [1]);

const cyclic = {};
cyclic.self = cyclic;
globalThis.fetch = async () => assert.fail("fetch must not run for serialization failure");
result = await client.request("Svc", "serialize", cyclic);
assert.equal(result.status, 1);
assert.match(result.msg, /serialize/i);
expectSeen(1);

globalThis.fetch = async () => json({ status: 2, code: 4002, msg: "stream business" }, 422);
result = await client.stream("Svc", "streamBusiness", undefined, () => {});
assert.deepEqual(result, { status: 2, code: 4002, msg: "stream business" });
expectSeen(2);

globalThis.fetch = async () => json({ status: 0 });
result = await client.stream("Svc", "wrongMedia", undefined, () => {});
assert.equal(result.status, 1);
assert.match(result.msg, /x-ndjson/);
expectSeen(1);

const bytes = new TextEncoder().encode('{"text":"你好"}\r\n\n{"n":2}');
const split = bytes.indexOf(0xe5) + 1;
globalThis.fetch = async () => new Response(new ReadableStream({
  start(controller) {
    controller.enqueue(bytes.slice(0, split));
    controller.enqueue(bytes.slice(split));
    controller.close();
  },
}), { headers: { "Content-Type": "application/x-ndjson; charset=utf-8" } });
const messages = [];
result = await client.stream("Svc", "valid", undefined, value => messages.push(value));
assert.equal(result.status, 0);
assert.deepEqual(messages, [{ text: "你好" }, { n: 2 }]);
expectSeen(0);

let malformedCancelled = false;
globalThis.fetch = async () => new Response(new ReadableStream({
  start(controller) { controller.enqueue(new TextEncoder().encode("bad\n")); },
  cancel() { malformedCancelled = true; },
}), { headers: { "Content-Type": "application/x-ndjson" } });
cleanupCheck = () => malformedCancelled;
result = await client.stream("Svc", "malformed", undefined, () => {});
assert.equal(result.status, 1);
assert.match(result.msg, /line 1/);
assert.equal(malformedCancelled, true);
expectSeen(1);

globalThis.fetch = async () => ndjson('{"n":1}\n');
result = await client.stream("Svc", "callback", undefined, () => { throw new Error("callback boom"); });
assert.equal(result.status, 1);
assert.match(result.msg, /callback boom/);
expectSeen(1);

globalThis.fetch = async () => new Response(new ReadableStream({
  pull(controller) { controller.error(new Error("read boom")); },
}), { headers: { "Content-Type": "application/x-ndjson" } });
result = await client.stream("Svc", "read", undefined, () => {});
assert.equal(result.status, 4);
assert.match(result.msg, /read boom/);
expectSeen(4);

globalThis.fetch = async () => ndjson(new Uint8Array([0xff, 0x0a]));
result = await client.stream("Svc", "utf8", undefined, () => {});
assert.equal(result.status, 1);
assert.match(result.msg, /UTF-8/);
expectSeen(1);

globalThis.fetch = async () => ndjson("");
result = await client.stream("Svc", "emptyStream", undefined, () => assert.fail("empty stream callback"));
assert.deepEqual(result, { status: 0 });
expectSeen(0);

globalThis.fetch = async () => new Response(null, {
  headers: { "Content-Type": "application/x-ndjson" },
});
result = await client.stream("Svc", "nullBody", undefined, () => assert.fail("null stream callback"));
assert.deepEqual(result, { status: 0 });
expectSeen(0);

const streamAbort = new AbortController();
streamAbort.abort();
globalThis.fetch = async () => { throw new DOMException("aborted", "AbortError"); };
result = await client.stream("Svc", "abortStream", undefined, () => {}, { signal: streamAbort.signal });
assert.equal(result.status, 4);
expectSeen(4);

const readAbort = new AbortController();
globalThis.fetch = async () => new Response(new ReadableStream({
  start(controller) {
    readAbort.signal.addEventListener("abort", () => controller.error(new DOMException("aborted", "AbortError")), { once: true });
    readAbort.abort();
  },
}), { headers: { "Content-Type": "application/x-ndjson" } });
result = await client.stream("Svc", "readAbort", undefined, () => {}, { signal: readAbort.signal });
assert.equal(result.status, 4);
assert.match(result.msg, /aborted/i);
expectSeen(4);

const readTimeout = new AbortController();
readTimeout.abort(new DOMException("timed out", "TimeoutError"));
globalThis.fetch = async () => new Response(new ReadableStream({
  pull(controller) { controller.error(new DOMException("aborted", "AbortError")); },
}), { headers: { "Content-Type": "application/x-ndjson" } });
result = await client.stream("Svc", "timeoutStream", undefined, () => {}, { signal: readTimeout.signal });
assert.equal(result.status, 5);
expectSeen(5);

const streamHookClient = new Client("http://example.test");
normalized = [];
streamHookClient.addRequestInterceptor(() => { throw new Error("stream hook"); });
streamHookClient.addResponseInterceptor((_s, _m, value) => { normalized.push(value.status); });
result = await streamHookClient.stream("Svc", "hook", undefined, () => {});
assert.equal(result.status, 1);
assert.deepEqual(normalized, [1]);
`
	scriptPath := filepath.Join(dir, "behavior.mjs")
	if err := os.WriteFile(scriptPath, []byte(script), 0o644); err != nil {
		t.Fatal(err)
	}
	command := exec.Command(node, "--experimental-strip-types", scriptPath)
	if output, err := command.CombinedOutput(); err != nil {
		t.Fatalf("TypeScript client behavior failed: %v\n%s", err, output)
	}
}

func TestTypeScriptClientStrictTypecheck(t *testing.T) {
	tsc, err := exec.LookPath("tsc")
	if err != nil {
		t.Skip("tsc is not installed")
	}
	dir := t.TempDir()
	path := filepath.Join(dir, "client.ts")
	if err := os.WriteFile(path, []byte(templateTs{}.genClientTemplate()), 0o644); err != nil {
		t.Fatal(err)
	}
	command := exec.Command(tsc,
		"--strict", "--noEmit", "--target", "ES2022", "--module", "ESNext", "--lib", "ES2022,DOM", path)
	if output, err := command.CombinedOutput(); err != nil {
		t.Fatalf("strict TypeScript check failed: %v\n%s", err, output)
	}
}

func TestTypeScriptClientSimplicityGate(t *testing.T) {
	source := templateTs{}.genClientTemplate() + templateTs{}.genServiceTemplate()
	for _, forbidden := range []string{"RpcError", "httpStatus", "result.aborted", "ClientErrorCode", "setTimeout"} {
		if strings.Contains(source, forbidden) {
			t.Errorf("generated TypeScript templates contain forbidden %q", forbidden)
		}
	}
	if !strings.Contains(source, "Promise<result<void>>") {
		t.Fatal("stream methods must resolve result<void>")
	}
}
