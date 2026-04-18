// Dynamic route matching + param extraction.
//
// The generated dispatch.mjs pre-compiles each Next dynamic route into a
// RegExp literal + ordered paramNames list. Runtime work is one linear
// scan until match — O(n) where n = number of dynamic routes. Every
// real-world app has fewer than a few hundred; a radix tree would save
// microseconds at the cost of a noticeably bigger bundle.

/**
 * Walk dynamicTable (pre-sorted by specificity at build time) and return
 * the first match along with extracted params. Null on no match.
 *
 * @returns {{ entry, params } | null}
 */
export function matchDynamic(pathname, dynamicTable) {
  for (const entry of dynamicTable) {
    const m = entry.pattern.exec(pathname);
    if (!m) continue;
    return {
      entry,
      params: paramsFromMatch(m, entry.paramNames),
    };
  }
  return null;
}

/**
 * Turn regex capture groups into a { name: value } params map. Catch-all
 * segments ([...foo], [[...foo]]) decode slashes so the route handler
 * receives the segments as-is.
 */
function paramsFromMatch(match, names) {
  const out = {};
  for (let i = 0; i < names.length; i++) {
    const raw = match[i + 1];
    if (raw === undefined) continue;
    try {
      out[names[i]] = decodeURIComponent(raw);
    } catch {
      // Malformed URL component — pass through raw. Handler decides.
      out[names[i]] = raw;
    }
  }
  return out;
}

/**
 * buildRouteContext packages the extracted params + URL bits into the
 * shape Next-compiled handlers expect on their second argument. Keeping
 * this separate from matchDynamic means the dispatcher can synthesize a
 * context for static matches too (with an empty params map).
 */
export function buildRouteContext(url, params) {
  return {
    params: params || {},
    searchParams: url.searchParams,
  };
}
