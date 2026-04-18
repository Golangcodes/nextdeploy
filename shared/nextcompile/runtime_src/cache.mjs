// Next.js cache + revalidation primitives.
//
// Matches the public surface of Next's `next/cache` module:
//   - revalidatePath(path, type?)    — mark a path as stale
//   - revalidateTag(tag)             — fan out to every path carrying the tag
//   - unstable_cache(fn, key, opts)  — memoize a server-only function
//
// The adapter wires `import "next/cache"` → this module via esbuild
// --alias, so user code imports work without source changes.
//
// Two-tier implementation:
//
//   Tier 1 (always available): in-memory Set scoped to this Worker instance.
//   Every incoming request checks against it before serving SSG/ISR from
//   R2. Survives for the lifetime of the Worker isolate (minutes to hours
//   on warm CF edges). Good enough for most apps.
//
//   Tier 2 (when env.NEXTCOMPILE_CACHE KV binding is present): writes a
//   stale timestamp under kv:"rev:<path>" so other Worker instances on
//   the same deployment see the invalidation. serve.mjs consults this
//   before serving a cached SSG/ISR asset.
//
// Both tiers are best-effort. True global cache invalidation requires the
// ISR revalidator (Queue + consumer worker + R2 rewrite) which is a
// separate milestone — this module is the API surface plus the local
// correctness layer.

import { getContext } from "./context.mjs";

// ── Tier 1: in-memory set ────────────────────────────────────────────────────
//
// Worker isolates may serve many requests between revalidate → subsequent
// GET, so a stale cache for this lifetime is visible to every request on
// this isolate even without KV.

const staleRoutes = new Set();
const staleTags = new Set();

// tagToPaths is populated from the manifest at module-init time (called
// from dispatcher.mjs) so revalidateTag can expand to its affected routes.
let tagIndex = null;

/**
 * Called once at worker init with the manifest.isr.tags map so tag-based
 * invalidation can fan out to the right paths.
 */
export function initCacheIndex(manifestISR) {
  tagIndex = (manifestISR && manifestISR.tags) || {};
}

// ── Public API matching next/cache ───────────────────────────────────────────

/**
 * revalidatePath marks the given path as stale in the local cache and (if
 * KV binding is available) writes a global stale marker.
 */
export async function revalidatePath(path, _type) {
  if (!path) return;
  staleRoutes.add(path);
  await persistToKV(`rev:${path}`);
}

/**
 * revalidateTag expands to every path registered with the tag at build
 * time and marks each as stale.
 */
export async function revalidateTag(tag) {
  if (!tag) return;
  staleTags.add(tag);
  await persistToKV(`revTag:${tag}`);
  if (tagIndex && Array.isArray(tagIndex[tag])) {
    for (const path of tagIndex[tag]) {
      staleRoutes.add(path);
      await persistToKV(`rev:${path}`);
    }
  }
}

/**
 * isStale is consulted by serve.mjs before returning a cached SSG/ISR asset.
 * Checks both tiers so a revalidation from any worker instance is visible
 * to any other instance (within KV consistency).
 */
export async function isStale(path, tags, env) {
  if (staleRoutes.has(path)) return true;
  if (Array.isArray(tags)) {
    for (const t of tags) if (staleTags.has(t)) return true;
  }
  if (env?.NEXTCOMPILE_CACHE) {
    if (await env.NEXTCOMPILE_CACHE.get(`rev:${path}`)) return true;
    if (Array.isArray(tags)) {
      for (const t of tags) {
        if (await env.NEXTCOMPILE_CACHE.get(`revTag:${t}`)) return true;
      }
    }
  }
  return false;
}

/**
 * unstable_cache is Next's memoization primitive. Our implementation is a
 * straight pass-through with an in-memory per-isolate Map keyed on the
 * caller's cache key. Tags are recorded so revalidateTag can clear the
 * entry. Matches the public surface; a real persistent cache is a
 * follow-up tied to ISR's KV story.
 */
const memoCache = new Map();

export function unstable_cache(fn, key, opts) {
  const keyHash = Array.isArray(key) ? key.join("|") : String(key);
  const tags = (opts && opts.tags) || [];
  const revalidate = opts && opts.revalidate;

  return async function cached(...args) {
    const fullKey = keyHash + "|" + JSON.stringify(args);
    const hit = memoCache.get(fullKey);
    const now = Date.now();
    if (hit && (!revalidate || now - hit.at < revalidate * 1000)) {
      // Tag-based invalidation beats TTL.
      let tagged = false;
      for (const t of tags) if (staleTags.has(t)) tagged = true;
      if (!tagged) return hit.value;
    }
    const value = await fn(...args);
    memoCache.set(fullKey, { value, at: now });
    return value;
  };
}

// ── Tier 2: KV binding persistence ───────────────────────────────────────────

async function persistToKV(key) {
  const ctx = safeContext();
  if (!ctx) return;
  const kv = ctx.env?.NEXTCOMPILE_CACHE;
  if (!kv) return;
  try {
    await kv.put(key, String(Date.now()), { expirationTtl: 24 * 60 * 60 });
  } catch {
    // KV put failures are non-fatal; tier 1 is still in effect.
  }
}

function safeContext() {
  try {
    return getContext();
  } catch {
    return null;
  }
}
