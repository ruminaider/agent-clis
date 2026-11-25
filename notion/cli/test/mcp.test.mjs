import assert from "node:assert/strict";
import { cp, mkdir, mkdtemp, rm, writeFile } from "node:fs/promises";
import { tmpdir } from "node:os";
import { dirname, join } from "node:path";
import { test } from "node:test";
import { fileURLToPath, pathToFileURL } from "node:url";

const CLI = join(dirname(fileURLToPath(import.meta.url)), "..");

// Each test imports a fresh copy of the CLI under a scratch home, so module state
// and credentials never touch the real ~/.config/notion-cli.
async function fixture(t) {
  const home = await mkdtemp(join(tmpdir(), "notion-mcp-"));
  t.after(() => rm(home, { recursive: true, force: true }));
  const cli = join(home, "cli");
  await cp(join(CLI, "lib"), join(cli, "lib"), { recursive: true });
  await writeFile(join(cli, "package.json"), JSON.stringify({ type: "module" }));
  return { home, cli, credentials: join(home, ".config", "notion-cli", "credentials.json") };
}

const encoder = new TextEncoder();

function reply(id, result) {
  return { jsonrpc: "2.0", id, result };
}

function event(message) {
  return `data: ${JSON.stringify(message)}\n\n`;
}

function sse(body) {
  return new Response(body, { headers: { "Content-Type": "text/event-stream" } });
}

function textResult(text) {
  return { content: [{ type: "text", text }] };
}

async function client(t, respond, onInitialize) {
  const f = await fixture(t);
  await mkdir(dirname(f.credentials), { recursive: true, mode: 0o700 });
  await writeFile(f.credentials, JSON.stringify({ access_token: "fixture-access" }), { mode: 0o600 });
  t.mock.method(globalThis, "fetch", async (url, { body, signal }) => {
    assert.equal(url, "https://mcp.notion.com/mcp");
    const request = JSON.parse(body);
    switch (request.method) {
      case "initialize":
        onInitialize?.();
        return sse(event(reply(request.id, {})));
      case "notifications/initialized":
        return new Response(null, { status: 204 });
      case "tools/call":
      case "tools/list":
        return respond(request, signal);
      default:
        assert.fail(`Unexpected method: ${request.method}`);
    }
  });

  // auth.js resolves its directory from os.homedir() at import time, which reads
  // the real HOME variable, so set it in place rather than replacing process.env.
  const home = process.env.HOME;
  process.env.HOME = f.home;
  try {
    return await import(pathToFileURL(join(f.cli, "lib", "mcp.js")).href);
  } finally {
    process.env.HOME = home;
  }
}

test("MCP accepts a JSON response only for the requested ID", async (t) => {
  const tools = [{ name: "notion-fetch" }];
  let offset = 0;
  const { listTools } = await client(t, ({ id }) => new Response(JSON.stringify(reply(id + offset++, { tools })), {
    headers: { "Content-Type": "Application/JSON; charset=utf-8" },
  }));
  assert.deepEqual(await listTools(), tools);
  await assert.rejects(listTools(), /No MCP response for request/);
});

test("MCP decodes a fragmented, multi-line SSE response", async (t) => {
  const { callTool } = await client(t, ({ id }) => {
    const framed = encoder.encode(`data: {"jsonrpc":"2.0",\r\ndata:"id":${id},"result":${JSON.stringify(textResult("café"))}}\r\n\r\n`);
    let offset = 0;
    return sse(new ReadableStream({
      pull(controller) {
        if (offset === framed.length) controller.close();
        else controller.enqueue(framed.subarray(offset, ++offset));
      },
    }));
  });
  assert.equal(await callTool("notion-fetch"), "café");
});

test("MCP skips notifications and other replies, then cancels SSE before EOF", async (t) => {
  let canceled = false;
  const { callTool } = await client(t, ({ id }) => {
    const notice = event({ jsonrpc: "2.0", method: "notifications/message" });
    const other = event(reply(id + 1, textResult("wrong reply")));
    const match = event(reply(id, textResult("matching reply")));
    let sent = false;
    return sse(new ReadableStream({
      pull(controller) {
        if (sent) controller.close();
        else {
          controller.enqueue(encoder.encode(notice + other + match + notice));
          sent = true;
        }
      },
      cancel() { canceled = true; },
    }, { highWaterMark: 0 }));
  });
  assert.equal(await callTool("notion-fetch"), "matching reply");
  assert.equal(canceled, true);
});

test("MCP aborts an open write stream on the shared deadline without retrying", { timeout: 3000 }, async (t) => {
  t.mock.timers.enable({ apis: ["setTimeout"] });
  const reading = Promise.withResolvers();
  let canceled = false;
  let writes = 0;
  const { callTool } = await client(t, (_request, signal) => {
    writes++;
    return sse(new ReadableStream({
      pull() { reading.resolve(signal); },
      cancel() { canceled = true; },
    }, { highWaterMark: 0 }));
  }, () => t.mock.timers.tick(40000));

  const outcome = callTool("notion-create-pages");
  void outcome.catch(() => {});
  const signal = await reading.promise;
  assert.ok(signal, "The write request needs an abort signal");
  t.mock.timers.tick(20000);
  await assert.rejects(outcome, /timed out.*outcome is unknown/);
  assert.equal(signal.aborted, true);
  assert.equal(canceled, true);
  assert.equal(writes, 1);
});

test("MCP bounds streamed response bodies without a Content-Length", async (t) => {
  for (const [type, status, limit] of [
    ["application/json", 200, 8 * 1024 * 1024],
    ["text/event-stream", 200, 8 * 1024 * 1024],
    ["text/plain", 502, 64 * 1024],
  ]) {
    await t.test(`${status} ${type}`, async (t) => {
      const chunk = new Uint8Array(64 * 1024).fill(32);
      let remaining = limit;
      let canceled = false;
      const { callTool } = await client(t, () => new Response(new ReadableStream({
        pull(controller) {
          if (remaining < 0) controller.close();
          else {
            controller.enqueue(chunk);
            remaining -= chunk.byteLength;
          }
        },
        cancel() { canceled = true; },
      }, { highWaterMark: 0 }), { status, headers: { "Content-Type": type } }));
      const expected = status === 200 ? /MCP response exceeds \d+ bytes/ : /MCP request failed \(502\): response body exceeds \d+ bytes/;
      await assert.rejects(callTool("notion-fetch"), (error) => {
        assert.match(error.message.slice(0, 120), expected);
        return true;
      });
      assert.equal(canceled, true);
    });
  }
});
