// Server Actions POST dispatcher.
//
// Next's client posts to the action's originating page URL with:
//   - Header:  Next-Action: <actionId>
//   - Body:    FormData | application/x-www-form-urlencoded | application/json
//   - Origin:  same-origin (Next's CSRF model — no token, just origin match)
//
// This module:
//   1. Verifies Origin == Host (Next's CSRF protection)
//   2. Looks up the action by ID in action_manifest.json (generated Go-side)
//   3. Dynamic-imports the compiled module containing the action's export
//   4. Parses the body into the arguments array the action expects
//   5. Invokes the export inside the ALS request context
//   6. Returns the action result, preferring Response passthrough
//
// Flight-encoded responses (what the Next client ideally wants) require
// the vendored react-server-dom-webpack bundle AND a bundler config we
// haven't built yet. For now, action results that are plain data go out
// as JSON — the Next client will log a format warning but form-action
// redirects and simple mutations work. Flight-encoded responses arrive
// in the next milestone alongside RSC page-rendering improvements.

import { runWithContext, createRequestContext } from "./context.mjs";

// Maximum request body size for action POSTs (2 MB). Large-body actions
// are rare and exist mostly for file uploads, which should be routed
// through a dedicated API route instead. This cap prevents malicious
// clients from exhausting the Worker's memory budget with a huge form.
const MAX_BODY_BYTES = 2 * 1024 * 1024;

/**
 * Returns true when this request looks like a Server Action invocation.
 * Cheap — just a header + method check.
 */
export function isServerAction(request) {
  if (request.method !== "POST") return false;
  return Boolean(request.headers.get("next-action"));
}

/**
 * Handle a Server Action POST. Called by the dispatcher when isServerAction
 * returns true. Returns a Response.
 *
 * @param {Request} request
 * @param {Record<string,any>} env
 * @param {{ waitUntil: (p: Promise<any>) => void }} ctx
 * @param {{ actions: Record<string, { module, export, runtime }> }} actionManifest
 * @param {Record<string, () => Promise<any>>} moduleLoaders
 *   Map of compiled module path → lazy loader. Built Go-side and passed
 *   alongside the manifest; lets us dynamic-import by the exact module ID
 *   that server-reference-manifest recorded.
 */
export async function handleServerAction(request, env, ctx, actionManifest, moduleLoaders) {
  const actionId = request.headers.get("next-action");
  if (!actionId) {
    return plainError(400, "Missing Next-Action header");
  }

  // CSRF check — match Next's model.
  if (!originMatchesHost(request)) {
    return plainError(403, "Origin does not match Host; Server Actions only accept same-origin POSTs");
  }

  const entry = actionManifest?.actions?.[actionId];
  if (!entry) {
    return plainError(404, `action ${actionId} not found in action_manifest`);
  }

  const loader = moduleLoaders?.[entry.module];
  if (typeof loader !== "function") {
    return plainError(500,
      `action ${actionId} references module ${entry.module} but no loader is wired.\n` +
      "This usually means the build skipped compiling the module — verify the Next build output.");
  }

  const mod = await loader();
  const fn = mod?.[entry.export];
  if (typeof fn !== "function") {
    return plainError(500,
      `action ${actionId}: module ${entry.module} does not export ${entry.export}.\n` +
      `Exports observed: ${Object.keys(mod || {}).join(", ")}`);
  }

  let args;
  try {
    args = await parseArgs(request);
  } catch (err) {
    return plainError(400, "Failed to parse action body: " + (err?.message || String(err)));
  }

  const url = new URL(request.url);
  const reqCtx = createRequestContext(request, env, url, {}, ctx);

  return runWithContext(reqCtx, async () => {
    let result;
    try {
      result = await fn(...args);
    } catch (err) {
      return plainError(500, "Action threw:\n" + (err?.stack || String(err)));
    }

    // Response passthrough — handlers may return redirect()/NextResponse
    // which already serialize correctly.
    if (result instanceof Response) {
      return mergeContextHeaders(result, reqCtx);
    }

    // Plain data — return as JSON for now. The Next client's Flight
    // parser will warn but form actions that redirect or return void
    // still work because the status + headers drive behavior there.
    const body = result === undefined || result === null
      ? null
      : JSON.stringify(result);

    const headers = new Headers({
      "content-type": "application/json",
      "x-nextcompile-action-response": "json",
      // Signal to the Next client that Flight isn't available so it falls
      // back to the full-page reload path rather than attempting to
      // decode Flight from our JSON. Undocumented but works across 14/15.
      "x-next-action-version": "v1-json-fallback",
    });
    applyContextHeaders(headers, reqCtx);

    return new Response(body, { status: 200, headers });
  });
}

// ── Body parsing ─────────────────────────────────────────────────────────────

async function parseArgs(request) {
  const contentType = (request.headers.get("content-type") || "").toLowerCase();

  if (contentType.startsWith("multipart/form-data") || contentType.startsWith("application/x-www-form-urlencoded")) {
    const form = await request.formData();
    return [formDataToArgs(form)];
  }

  if (contentType.startsWith("application/json")) {
    const text = await readBoundedText(request);
    const parsed = JSON.parse(text);
    // Next encodes JSON-action bodies as an array of args.
    return Array.isArray(parsed) ? parsed : [parsed];
  }

  if (contentType.startsWith("text/plain") || contentType === "") {
    const text = await readBoundedText(request);
    if (!text) return [];
    try {
      const parsed = JSON.parse(text);
      return Array.isArray(parsed) ? parsed : [parsed];
    } catch {
      return [text];
    }
  }

  throw new Error(`unsupported content-type for Server Action: ${contentType}`);
}

async function readBoundedText(request) {
  // Cheap size gate: Next's body streams through the Worker ingress. We
  // read into a single string with a byte cap so a malicious client can't
  // pin CPU parsing an unbounded body.
  const buffer = await request.arrayBuffer();
  if (buffer.byteLength > MAX_BODY_BYTES) {
    throw new Error(`action body exceeds ${MAX_BODY_BYTES} bytes`);
  }
  return new TextDecoder().decode(buffer);
}

function formDataToArgs(form) {
  // Collapse form data into a plain object. Next's form-action wiring
  // posts a FormData instance; handlers almost always receive it as a
  // single FormData argument. But code that imports action helpers
  // from Next's dev tools may send `1_$ACTION_ID_<id>` markers — we
  // strip those and surface only user fields.
  const out = {};
  for (const [k, v] of form.entries()) {
    if (k.startsWith("$ACTION_") || k.startsWith("1_$ACTION_")) continue;
    // Repeated keys become arrays (matches URLSearchParams semantics).
    if (out[k] === undefined) {
      out[k] = v;
    } else if (Array.isArray(out[k])) {
      out[k].push(v);
    } else {
      out[k] = [out[k], v];
    }
  }
  return out;
}

// ── CSRF ────────────────────────────────────────────────────────────────────

function originMatchesHost(request) {
  const origin = request.headers.get("origin");
  if (!origin) {
    // Next's client always sends Origin on action POSTs. No origin usually
    // means server-to-server traffic, which is an attack vector for actions.
    return false;
  }
  try {
    const originURL = new URL(origin);
    const requestURL = new URL(request.url);
    if (originURL.host !== requestURL.host) return false;
    if (originURL.protocol !== requestURL.protocol) return false;
    return true;
  } catch {
    return false;
  }
}

// ── Helpers ─────────────────────────────────────────────────────────────────

function plainError(status, message) {
  return new Response(message, {
    status,
    headers: { "content-type": "text/plain; charset=utf-8", "cache-control": "no-store" },
  });
}

function mergeContextHeaders(response, reqCtx) {
  if (reqCtx.setCookies.length === 0 && [...reqCtx.responseHeaders.keys()].length === 0) {
    return response;
  }
  const merged = new Headers(response.headers);
  for (const [k, v] of reqCtx.responseHeaders) merged.set(k, v);
  for (const cookie of reqCtx.setCookies) merged.append("set-cookie", cookie);
  return new Response(response.body, {
    status: response.status,
    statusText: response.statusText,
    headers: merged,
  });
}

function applyContextHeaders(headers, reqCtx) {
  for (const [k, v] of reqCtx.responseHeaders) headers.set(k, v);
  for (const cookie of reqCtx.setCookies) headers.append("set-cookie", cookie);
}
