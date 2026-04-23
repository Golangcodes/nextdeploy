// Per-request context propagation via AsyncLocalStorage.
//
// Next 15 moved cookies() / headers() / draftMode() to async — they now
// return Promises, but the *values* they resolve to are still scoped to
// the request that's being handled. ALS is the standard primitive for
// request-scoped state on the Node runtime; Cloudflare Workers provides
// it through `nodejs_compat_v2`.
//
// Contract enforced here:
//   - Every page / api / action / middleware handler must run inside
//     runWithContext(ctx, fn). The dispatcher does this unconditionally
//     on every invocation — individual handlers never call it.
//   - cookies() / headers() / draftMode() throw if called outside a
//     request scope. Better to surface "you called this at module top
//     level" loudly than to silently return stale values.
//
// The Request / URL / env are captured at the top of fetch() so the
// accessors don't have to thread them through every call.

import { AsyncLocalStorage } from "node:async_hooks";

/**
 * @typedef {{
 *   request: Request,
 *   env: Record<string, any>,
 *   url: URL,
 *   params: Record<string, string>,
 *   // Draft mode flag — mutated by draftMode().enable()/disable()
 *   draft: { enabled: boolean },
 *   // Mutable response headers the dispatcher will merge into the final Response.
 *   responseHeaders: Headers,
 *   // Cookies the handler adds via cookies().set() — applied on response build.
 *   setCookies: string[],
 * }} RequestContext
 */

const als = new AsyncLocalStorage();

/**
 * Run fn with the given RequestContext active for the duration. Any
 * awaited work inside fn, and any transitively-awaited work, will see
 * the context via getContext(). This is the single primitive that lets
 * cookies() / headers() work from deep in a page render.
 */
export function runWithContext(ctx, fn) {
  return als.run(ctx, fn);
}

/**
 * Access the active RequestContext. Throws if called outside runWithContext —
 * this prevents accidental module-top-level usage that would otherwise
 * silently leak state between requests.
 */
export function getContext() {
  const ctx = als.getStore();
  if (!ctx) {
    throw new Error(
      "nextcompile: cookies/headers/draftMode called outside a request scope. " +
        "This usually means the call is at module top level — move it inside " +
        "an async handler or server component.",
    );
  }
  return ctx;
}

/**
 * Build the RequestContext a dispatcher hands to runWithContext. Keeps
 * the construction shape in one place so handlers that need to extend
 * context (e.g. action dispatch) don't drift.
 */
export function createRequestContext(request, env, url, params, ctx) {
  return {
    request,
    env,
    url,
    params: params || {},
    draft: { enabled: false },
    responseHeaders: new Headers(),
    setCookies: [],
    // workerCtx provides waitUntil — required by after(). Passing it here
    // (instead of fishing it out globally) keeps the primitive testable.
    workerCtx: ctx || null,
  };
}

/**
 * Next 14.2+: `after(fn)` queues fn to run post-response. On Cloudflare
 * Workers this hooks into ctx.waitUntil so the runtime keeps the isolate
 * alive until fn settles, even after the Response has been sent to the
 * client.
 *
 * fn may return a Promise; nextcompile awaits it inside waitUntil. Errors
 * are swallowed (matching Next's semantics — after() is fire-and-forget)
 * but logged to console so operators can see them in Workers tail logs.
 */
export function after(fn) {
  if (typeof fn !== "function") {
    throw new TypeError("after() expects a function argument");
  }
  const ctx = getContext();
  if (!ctx.workerCtx || typeof ctx.workerCtx.waitUntil !== "function") {
    // No waitUntil (tests, or weird invocation path) — run synchronously
    // as a best effort. The promise may be dropped if the request ends
    // before it settles, which is the same failure mode Next exhibits
    // when after() is called outside an HTTP request.
    Promise.resolve()
      .then(() => fn())
      .catch((err) => console.error("after() threw:", err));
    return;
  }
  ctx.workerCtx.waitUntil(
    Promise.resolve()
      .then(() => fn())
      .catch((err) => console.error("after() threw:", err)),
  );
}

// ── Next.js public async APIs ────────────────────────────────────────────────
//
// These mirror the Next 15 signatures. Next's compiled page code imports
// from "next/headers"; esbuild's --alias flag (set by the adapter build
// step) redirects those imports to this module so the calls wire through
// our ALS instead of Next's private runtime.

/**
 * Next 15: async. Returns a RequestCookies-like object for reading the
 * incoming Cookie header, and a ResponseCookies-like API for setting
 * cookies on the response.
 */
export async function cookies() {
  const ctx = getContext();
  return new RequestCookiesImpl(ctx);
}

/**
 * Next 15: async. Returns a read-only Headers view of the incoming
 * request headers. Mutation methods throw — matching Next's semantics,
 * which warn then no-op in dev and throw in prod.
 */
export async function headers() {
  const ctx = getContext();
  return new ReadonlyHeadersImpl(ctx.request.headers);
}

/**
 * Next 15: async. Returns a DraftMode handle with enable()/disable()/isEnabled.
 * Draft mode in Next flips a cookie; we store the flag on context and the
 * dispatcher applies it on response build.
 */
export async function draftMode() {
  const ctx = getContext();
  return {
    get isEnabled() {
      return ctx.draft.enabled;
    },
    enable() {
      ctx.draft.enabled = true;
      ctx.setCookies.push(
        "__prerender_bypass=1; Path=/; HttpOnly; SameSite=Lax; Secure",
      );
    },
    disable() {
      ctx.draft.enabled = false;
      ctx.setCookies.push(
        "__prerender_bypass=; Path=/; HttpOnly; SameSite=Lax; Max-Age=0",
      );
    },
  };
}

// ── Implementations ──────────────────────────────────────────────────────────

class RequestCookiesImpl {
  constructor(ctx) {
    this._ctx = ctx;
    this._cache = null;
  }
  _parse() {
    if (this._cache) return this._cache;
    const header = this._ctx.request.headers.get("cookie") || "";
    const map = new Map();
    for (const piece of header.split(/;\s*/)) {
      if (!piece) continue;
      const idx = piece.indexOf("=");
      if (idx < 0) continue;
      const name = decodeURIComponent(piece.slice(0, idx).trim());
      const value = decodeURIComponent(piece.slice(idx + 1).trim());
      if (!map.has(name)) map.set(name, value);
    }
    this._cache = map;
    return map;
  }
  get(name) {
    const v = this._parse().get(name);
    return v === undefined ? undefined : { name, value: v };
  }
  has(name) {
    return this._parse().has(name);
  }
  getAll(name) {
    const v = this._parse().get(name);
    if (!name) {
      const out = [];
      for (const [n, val] of this._parse()) out.push({ name: n, value: val });
      return out;
    }
    return v === undefined ? [] : [{ name, value: v }];
  }
  set(name, value, options = {}) {
    this._cache = null; // subsequent gets should reflect the write
    const attrs = [];
    if (options.path) attrs.push(`Path=${options.path}`);
    else attrs.push("Path=/");
    if (options.domain) attrs.push(`Domain=${options.domain}`);
    if (options.maxAge !== undefined) attrs.push(`Max-Age=${options.maxAge}`);
    if (options.expires)
      attrs.push(`Expires=${options.expires instanceof Date ? options.expires.toUTCString() : options.expires}`);
    if (options.httpOnly !== false) attrs.push("HttpOnly");
    if (options.secure !== false) attrs.push("Secure");
    if (options.sameSite) attrs.push(`SameSite=${options.sameSite}`);
    else attrs.push("SameSite=Lax");
    this._ctx.setCookies.push(
      `${encodeURIComponent(name)}=${encodeURIComponent(value)}; ${attrs.join("; ")}`,
    );
  }
  delete(name, options = {}) {
    this.set(name, "", { ...options, maxAge: 0 });
  }
}

class ReadonlyHeadersImpl {
  constructor(headers) {
    this._h = headers;
  }
  get(name) {
    return this._h.get(name);
  }
  has(name) {
    return this._h.has(name);
  }
  keys() {
    return this._h.keys();
  }
  values() {
    return this._h.values();
  }
  entries() {
    return this._h.entries();
  }
  forEach(cb) {
    return this._h.forEach(cb);
  }
  [Symbol.iterator]() {
    return this._h[Symbol.iterator]();
  }
  // Mutation methods — match Next's runtime error.
  append() {
    throw new Error("headers() returns a read-only Headers. Set response headers on the Response object instead.");
  }
  set() {
    throw new Error("headers() returns a read-only Headers. Set response headers on the Response object instead.");
  }
  delete() {
    throw new Error("headers() returns a read-only Headers. Set response headers on the Response object instead.");
  }
}
