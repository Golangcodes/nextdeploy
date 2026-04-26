// Shim target for `import "next/server"`. Covers the public surface
// Server Components + Actions + route handlers actually use:
//
//   - after(fn)                   queues work via ctx.waitUntil
//   - NextRequest / NextResponse  thin wrappers over the Web Request/Response
//   - userAgent(request)          parses UA headers (trimmed: we return
//                                 an object with the bare fields Next surfaces)
//   - ipAddress(request)          reads CF-Connecting-IP / X-Forwarded-For
//
// Only what's commonly imported by app code. Exotic surface (like the
// Next router internals that live in this module) isn't something user
// code touches; we return stubs that throw if reached.

export { after } from "../context.mjs";

// NextResponse — mostly a convenience wrapper around Response with a few
// static factories. Our version mirrors the public API so app code like
// `return NextResponse.json({...})` or `NextResponse.redirect(url)`
// resolves against this shim without behavior change.
export class NextResponse extends Response {
  static json(data, init) {
    return Response.json(data, init);
  }
  static redirect(url, statusOrInit) {
    return Response.redirect(String(url), typeof statusOrInit === "number" ? statusOrInit : 307);
  }
  static rewrite(url, init) {
    // Matches Next's middleware contract: the dispatcher inspects this
    // header to rewrite the request URL and continue, rather than
    // short-circuit with the (empty) response body.
    const headers = new Headers(init?.headers);
    headers.set("x-middleware-rewrite", String(url));
    return new NextResponse(null, { ...(init || {}), status: 200, headers });
  }
  static next(init) {
    // x-middleware-next:1 tells the dispatcher "continue to the route
    // handler." Without the header, an empty 200 response would be
    // treated as a short-circuit (see dispatcher.runMiddlewareStack).
    const headers = new Headers(init?.headers);
    headers.set("x-middleware-next", "1");
    return new NextResponse(null, { ...(init || {}), status: 200, headers });
  }
}

// NextRequest — extends Request with cookie/geo/ip accessors that Next's
// types expose. Keep it minimal; apps rarely need more than cookies.
export class NextRequest extends Request {
  get cookies() {
    return {
      get: (name) => {
        const header = this.headers.get("cookie") || "";
        const target = encodeURIComponent(name);
        for (const pair of header.split(/;\s*/)) {
          const [k, v] = pair.split("=");
          if (k?.trim() === target) return { name, value: decodeURIComponent(v || "") };
        }
        return undefined;
      },
    };
  }
  get ip() {
    return this.headers.get("cf-connecting-ip") || this.headers.get("x-forwarded-for") || "";
  }
  get geo() {
    return {
      country: this.headers.get("cf-ipcountry") || undefined,
      city: this.headers.get("cf-ipcity") || undefined,
      region: this.headers.get("cf-region") || undefined,
    };
  }
}

export function userAgent(request) {
  const ua = request.headers.get("user-agent") || "";
  return {
    ua,
    // Coarse bot detection — matches Next's exported shape.
    isBot: /bot|crawler|spider|slurp/i.test(ua),
    browser: {},
    device: {},
    engine: {},
    os: {},
    cpu: {},
  };
}

export function ipAddress(request) {
  return (
    request.headers.get("cf-connecting-ip") ||
    request.headers.get("x-forwarded-for") ||
    request.headers.get("x-real-ip") ||
    ""
  );
}
