// /_next/image handler — Next.js's image optimization endpoint.
//
// Contract:
//   GET /_next/image?url=<src>&w=<width>&q=<quality>[&fm=<format>]
//
// Behavior:
//   1. Validate src against manifest.images.remotePatterns (anti-SSRF).
//   2. If env.IMAGES binding exists (Cloudflare Images), use its transform
//      API — best-quality output with native edge transcoding.
//   3. Otherwise fall back to fetch+return: pass the upstream image through
//      untransformed with a reasonable cache policy. This preserves correctness
//      at the cost of bandwidth — Next's client will still display the image.
//
// Security: every hostname must match a remotePatterns entry. No exceptions.
// Next's default is deny-all for external domains until the app opts in.

const DEFAULT_CACHE_TTL = 60 * 60 * 24; // 24h

/**
 * @param {Request} request
 * @param {Record<string, any>} env
 * @param {{ images: { remotePatterns, domains, formats, unoptimized } }} manifest
 */
export async function handleImageRequest(request, env, manifest) {
  const url = new URL(request.url);
  const src = url.searchParams.get("url");
  if (!src) {
    return textError(400, "missing ?url= parameter");
  }

  const width = parseIntOrDefault(url.searchParams.get("w"), 0);
  const quality = clamp(parseIntOrDefault(url.searchParams.get("q"), 75), 1, 100);
  const format = url.searchParams.get("fm") || pickPreferredFormat(manifest);

  // Relative URLs are same-origin — always allowed.
  const isAbsolute = /^https?:\/\//i.test(src);

  if (isAbsolute) {
    if (!passesRemotePatternCheck(src, manifest)) {
      return textError(403, "url does not match any remotePatterns / domains entry in next.config");
    }
  }

  const resolved = isAbsolute ? src : new URL(src, request.url).toString();

  if (manifest?.images?.unoptimized) {
    return passthroughFetch(resolved);
  }

  if (typeof env?.IMAGES?.input === "function") {
    return await transformWithCloudflareImages(env.IMAGES, resolved, {
      width,
      quality,
      format,
    });
  }

  return passthroughFetch(resolved);
}

function passesRemotePatternCheck(src, manifest) {
  const images = manifest?.images;
  if (!images) return false;
  const srcURL = parseURLSafe(src);
  if (!srcURL) return false;
  if (matchesLegacyDomains(srcURL, images.domains)) return true;
  return matchesRemotePatterns(srcURL, images.remotePatterns);
}

function parseURLSafe(src) {
  try {
    return new URL(src);
  } catch {
    return null;
  }
}

/**
 * Legacy Next 12–13 `images.domains` — exact hostname match.
 */
function matchesLegacyDomains(srcURL, domains) {
  if (!Array.isArray(domains)) return false;
  return domains.some((d) => d && srcURL.hostname === d);
}

/**
 * Next 14+ `images.remotePatterns` — all declared fields (protocol,
 * hostname, port, pathname) must match. Missing fields pass through.
 */
function matchesRemotePatterns(srcURL, patterns) {
  if (!Array.isArray(patterns)) return false;
  return patterns.some((p) => remotePatternMatches(srcURL, p));
}

function remotePatternMatches(srcURL, p) {
  if (p.protocol && !srcURL.protocol.startsWith(p.protocol)) return false;
  if (p.hostname && !hostnameMatches(srcURL.hostname, p.hostname)) return false;
  if (p.port && srcURL.port !== p.port) return false;
  if (p.pathname && !pathnameMatches(srcURL.pathname, p.pathname)) return false;
  return true;
}

/**
 * Next's remotePatterns support glob-like hostnames: "**.example.com".
 */
function hostnameMatches(host, pattern) {
  if (pattern === host) return true;
  if (pattern.startsWith("**.")) {
    const suffix = pattern.slice(2);
    return host.endsWith(suffix) || host === suffix.slice(1);
  }
  if (pattern.startsWith("*.")) {
    const suffix = pattern.slice(1);
    // Exactly one subdomain level.
    const rest = host.slice(0, host.length - suffix.length);
    return host.endsWith(suffix) && !rest.includes(".");
  }
  return false;
}

function pathnameMatches(pathname, pattern) {
  if (pattern === pathname) return true;
  if (pattern.endsWith("/**")) {
    return pathname.startsWith(pattern.slice(0, -2));
  }
  if (pattern.endsWith("/*")) {
    const base = pattern.slice(0, -1);
    return pathname.startsWith(base) && !pathname.slice(base.length).includes("/");
  }
  return false;
}

/**
 * Cloudflare Images binding path. env.IMAGES.input() returns a chainable
 * transform pipeline. This is the lowest-cost, highest-quality path when
 * the binding is configured — transforms happen natively at the edge.
 */
async function transformWithCloudflareImages(images, src, { width, quality, format }) {
  const upstream = await fetch(src, { cf: { cacheTtl: DEFAULT_CACHE_TTL } });
  if (!upstream.ok) {
    return textError(upstream.status, `upstream image fetch failed: ${upstream.statusText}`);
  }

  let pipeline = images.input(upstream.body);
  const tx = {};
  if (width > 0) tx.width = width;
  if (quality !== 75) tx.quality = quality;
  if (format && format !== "auto") tx.format = format;
  if (Object.keys(tx).length > 0) pipeline = pipeline.transform(tx);

  const result = await pipeline.output({ format });
  const resp = result.response();
  const headers = new Headers(resp.headers);
  if (!headers.has("cache-control")) {
    headers.set("cache-control", `public, max-age=${DEFAULT_CACHE_TTL}, immutable`);
  }
  return new Response(resp.body, { status: resp.status, headers });
}

/**
 * No-binding fallback: fetch and pipe the upstream image through. Preserves
 * correctness but loses optimization. Operators should configure an IMAGES
 * binding for production.
 */
async function passthroughFetch(src) {
  const upstream = await fetch(src, { cf: { cacheTtl: DEFAULT_CACHE_TTL } });
  if (!upstream.ok) {
    return textError(upstream.status, `upstream image fetch failed: ${upstream.statusText}`);
  }
  const headers = new Headers(upstream.headers);
  if (!headers.has("cache-control")) {
    headers.set("cache-control", `public, max-age=${DEFAULT_CACHE_TTL}`);
  }
  headers.set("x-nextcompile-image", "passthrough");
  return new Response(upstream.body, { status: upstream.status, headers });
}

function pickPreferredFormat(manifest) {
  const list = manifest?.images?.formats;
  if (!Array.isArray(list) || list.length === 0) return "auto";
  // Next publishes MIME types here ("image/avif", "image/webp"). Strip
  // to the short form the transform pipelines expect.
  const short = list[0].replace(/^image\//, "");
  return short || "auto";
}

function parseIntOrDefault(s, d) {
  if (!s) return d;
  const n = parseInt(s, 10);
  return Number.isFinite(n) ? n : d;
}

function clamp(n, lo, hi) {
  return Math.max(lo, Math.min(hi, n));
}

function textError(status, message) {
  return new Response(message, {
    status,
    headers: { "content-type": "text/plain; charset=utf-8", "cache-control": "no-store" },
  });
}
