// NextDeploy Cloudflare Worker shim for Next.js standalone builds.
//
// Bridges Cloudflare Workers' fetch handler to Next.js's Node-style
// request handler. Requires `nodejs_compat_v2` compatibility flag.
//
// Limitations of v1:
//   - Response is buffered (no streaming SSR)
//   - WebSockets unsupported
//   - ISR revalidate is best-effort (writes to in-memory cache only)
//   - Image optimization not handled (route to /_next/image returns 501)
//
// Bindings expected:
//   - env.ASSETS — R2 bucket holding /_next/static/* and /public/* assets

import { Readable } from "node:stream";
import { createServer } from "node:http";
import path from "node:path";

// Lazy-imported on first request — Next's startup is heavy.
let nextHandlerPromise = null;

async function getNextHandler() {
  if (nextHandlerPromise) return nextHandlerPromise;
  nextHandlerPromise = (async () => {
    const NextServerModule = await import("next/dist/server/next-server.js");
    const NextServer = NextServerModule.default || NextServerModule;
    const requiredServerFiles = await import(
      "./.next/required-server-files.json",
      { with: { type: "json" } }
    );
    const conf = requiredServerFiles.default?.config ?? requiredServerFiles.config ?? {};
    const server = new NextServer({
      hostname: "0.0.0.0",
      port: 8787,
      dir: path.resolve("."),
      dev: false,
      conf,
      customServer: false,
    });
    return server.getRequestHandler();
  })();
  return nextHandlerPromise;
}

// Convert a Workers Request into a Node IncomingMessage-compatible object.
function webRequestToNodeReq(request) {
  const url = new URL(request.url);
  const bodyStream = request.body
    ? Readable.fromWeb(request.body)
    : Readable.from([]);

  // Augment the readable with the IncomingMessage surface Next touches.
  const headers = {};
  for (const [k, v] of request.headers.entries()) headers[k.toLowerCase()] = v;

  const req = bodyStream;
  req.method = request.method;
  req.url = url.pathname + url.search;
  req.headers = headers;
  req.httpVersion = "1.1";
  req.httpVersionMajor = 1;
  req.httpVersionMinor = 1;
  req.complete = false;
  req.socket = { remoteAddress: headers["cf-connecting-ip"] || "0.0.0.0" };
  req.connection = req.socket;
  return req;
}

// Build a Node ServerResponse-compatible object that resolves a Web Response.
function createNodeRes() {
  let statusCode = 200;
  let statusMessage = "OK";
  const headersOut = {};
  const chunks = [];
  let finished = false;
  let resolveResponse;
  const responsePromise = new Promise((r) => (resolveResponse = r));

  const res = {
    statusCode: 200,
    statusMessage: "OK",
    headersSent: false,
    finished: false,
    writableEnded: false,

    setHeader(name, value) {
      headersOut[String(name).toLowerCase()] = value;
      return this;
    },
    getHeader(name) {
      return headersOut[String(name).toLowerCase()];
    },
    getHeaders() {
      return { ...headersOut };
    },
    hasHeader(name) {
      return String(name).toLowerCase() in headersOut;
    },
    removeHeader(name) {
      delete headersOut[String(name).toLowerCase()];
    },
    writeHead(code, msgOrHeaders, maybeHeaders) {
      statusCode = code;
      let hdrs;
      if (typeof msgOrHeaders === "string") {
        statusMessage = msgOrHeaders;
        hdrs = maybeHeaders;
      } else {
        hdrs = msgOrHeaders;
      }
      if (hdrs) {
        for (const [k, v] of Array.isArray(hdrs)
          ? entriesFromArray(hdrs)
          : Object.entries(hdrs)) {
          headersOut[String(k).toLowerCase()] = v;
        }
      }
      this.headersSent = true;
      this.statusCode = code;
      return this;
    },
    write(chunk, encoding) {
      if (chunk == null) return true;
      if (typeof chunk === "string") {
        chunks.push(new TextEncoder().encode(chunk));
      } else if (chunk instanceof Uint8Array) {
        chunks.push(chunk);
      } else if (chunk?.buffer) {
        chunks.push(new Uint8Array(chunk.buffer, chunk.byteOffset, chunk.byteLength));
      }
      return true;
    },
    end(chunk, encoding, cb) {
      if (typeof chunk === "function") {
        cb = chunk;
        chunk = undefined;
      } else if (typeof encoding === "function") {
        cb = encoding;
        encoding = undefined;
      }
      if (chunk != null) this.write(chunk, encoding);
      if (!finished) {
        finished = true;
        this.finished = true;
        this.writableEnded = true;
        const body = chunks.length === 0 ? null : concatChunks(chunks);
        const headers = new Headers();
        for (const [k, v] of Object.entries(headersOut)) {
          if (Array.isArray(v)) for (const vv of v) headers.append(k, String(vv));
          else if (v != null) headers.set(k, String(v));
        }
        resolveResponse(
          new Response(body, { status: statusCode, statusText: statusMessage, headers }),
        );
      }
      if (cb) cb();
    },
    // Node EventEmitter surface — Next attaches close/error listeners.
    on() { return this; },
    once() { return this; },
    off() { return this; },
    addListener() { return this; },
    removeListener() { return this; },
    removeAllListeners() { return this; },
    emit() { return false; },
    flushHeaders() { this.headersSent = true; },
    socket: null,
    connection: null,
  };
  return { res, responsePromise };
}

function concatChunks(chunks) {
  let total = 0;
  for (const c of chunks) total += c.byteLength;
  const out = new Uint8Array(total);
  let off = 0;
  for (const c of chunks) {
    out.set(c, off);
    off += c.byteLength;
  }
  return out;
}

function* entriesFromArray(arr) {
  for (let i = 0; i < arr.length; i += 2) yield [arr[i], arr[i + 1]];
}

// Try to serve from R2 ASSETS binding. Returns null if not present.
async function tryServeStatic(env, pathname) {
  if (!env.ASSETS) return null;

  let key = pathname.replace(/^\/+/, "");
  if (pathname.startsWith("/_next/static/")) {
    // _next/static/* → already keyed under _next/static/* in R2.
  } else if (pathname.startsWith("/")) {
    // Try /public/* layout first; collectS3Assets uploads public files at root.
  }
  if (!key) return null;

  const obj = await env.ASSETS.get(key);
  if (!obj) return null;

  const headers = new Headers();
  obj.writeHttpMetadata?.(headers);
  if (obj.httpEtag) headers.set("etag", obj.httpEtag);
  if (!headers.has("cache-control")) {
    headers.set(
      "cache-control",
      pathname.startsWith("/_next/static/")
        ? "public, max-age=31536000, immutable"
        : "public, max-age=300",
    );
  }
  return new Response(obj.body, { headers });
}

export default {
  async fetch(request, env, ctx) {
    const url = new URL(request.url);

    // Static asset short-circuit — skip waking Next at all.
    if (
      url.pathname.startsWith("/_next/static/") ||
      url.pathname.startsWith("/public/")
    ) {
      const staticRes = await tryServeStatic(env, url.pathname);
      if (staticRes) return staticRes;
    }

    // Image optimization not yet implemented in v1 — fail loud.
    if (url.pathname === "/_next/image") {
      return new Response(
        "image optimization not implemented in nextdeploy cloudflare adapter v1",
        { status: 501 },
      );
    }

    // Push secrets into process.env so Next code can read them.
    for (const k of Object.keys(env)) {
      const v = env[k];
      if (typeof v === "string" && process.env[k] === undefined) {
        process.env[k] = v;
      }
    }

    let handler;
    try {
      handler = await getNextHandler();
    } catch (err) {
      return new Response("next handler init failed: " + (err?.stack || err), {
        status: 500,
      });
    }

    const req = webRequestToNodeReq(request);
    const { res, responsePromise } = createNodeRes();
    try {
      await handler(req, res);
    } catch (err) {
      if (!res.writableEnded) {
        res.statusCode = 500;
        res.end("next handler error: " + (err?.message || err));
      }
    }
    return responsePromise;
  },
};
