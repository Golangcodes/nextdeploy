// Static + SSG asset serving from R2.
//
// Conventions upheld by the nextdeploy packager when uploading to R2:
//   - /_next/static/<hash>/...   →  keyed under "_next/static/<hash>/..."
//   - /public/foo.png            →  keyed under "foo.png"  (Next serves
//                                   /public content at the root)
//   - SSG/ISR HTML               →  keyed under the file path from the
//                                   manifest (e.g. "index.html",
//                                   "blog.html", "news.html")
//
// env.ASSETS is the R2 binding for the public bundle; it's declared
// automatically by cloudflare_bindings.go when compute routes exist.

/**
 * Serve a /_next/static or /public path from R2. Returns null on miss so
 * the dispatcher can fall through to a 404 or compute route.
 */
export async function serveStaticFromR2(env, pathname) {
  if (!env.ASSETS) return null;

  const key = r2KeyForStatic(pathname);
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

/**
 * Serve a pre-rendered HTML file (SSG or ISR) from R2. The dispatcher
 * resolves the R2 key from manifest.routes.ssg/isr and passes it here.
 */
export async function serveSSGFromR2(env, htmlKey) {
  if (!env.ASSETS) return null;
  // Manifest entries are typically "/foo.html" — normalize to the bare key.
  const key = htmlKey.replace(/^\/+/, "");
  const obj = await env.ASSETS.get(key);
  if (!obj) return null;

  const headers = new Headers();
  obj.writeHttpMetadata?.(headers);
  if (!headers.has("content-type")) {
    headers.set("content-type", "text/html; charset=utf-8");
  }
  if (!headers.has("cache-control")) {
    headers.set("cache-control", "public, max-age=0, must-revalidate");
  }
  return new Response(obj.body, { headers });
}

function r2KeyForStatic(pathname) {
  if (pathname.startsWith("/_next/static/")) {
    return pathname.slice(1); // drop leading slash
  }
  if (pathname.startsWith("/public/")) {
    return pathname.slice("/public/".length);
  }
  // Next also serves public/* at root — these only reach here via a
  // caller that hinted "this is a public asset". Keep the contract tight:
  // return null so we don't accidentally 200 on non-static paths.
  return null;
}
