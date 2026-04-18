// React Server Components rendering.
//
// Bridge from a compiled Next App Router page to a streaming HTML Response.
// Delegates Flight encoding to the vendored react-server-dom-webpack bundle
// (see runtime/vendor/README.md for the vendoring contract).
//
// Pipeline:
//   1. Check PPR — not yet implemented; return 501 with clear message.
//   2. Load the page module, its layout chain, and its client manifest
//      in parallel.
//   3. Compose layouts root → leaf → page.
//   4. Pass composed tree + clientModules to renderToReadableStream.
//   5. Wrap the resulting Flight stream in a minimal HTML shell so
//      browsers get something renderable on first paint.
//   6. Emit the response with context-propagated headers/cookies.
//
// Scope intentionally narrow:
//   - Layouts are assumed to be plain React components taking { children }.
//     Suspense boundaries and loading.js interleave come with proper
//     streaming protocol work.
//   - Client manifest is passed verbatim as Flight's bundlerConfig.
//     `clientModules` is the shape Next emits; renderToReadableStream
//     consumes it directly.
//   - HTML shell is minimal. Full Next-style shell (hydrator bootstrap,
//     chunk preloads, RSC protocol tags) arrives with the next RSC
//     milestone. For now it's enough to get HTML into a browser.

import { runWithContext, createRequestContext } from "./context.mjs";

let vendoredLoader = null;
let vendoredLoadError = null;

async function loadVendored() {
  if (vendoredLoader) return vendoredLoader;
  if (vendoredLoadError) return null;
  try {
    vendoredLoader = await import("./vendor/react-server-dom-webpack/server.edge.mjs");
    return vendoredLoader;
  } catch (err) {
    vendoredLoadError = err;
    return null;
  }
}

/**
 * Render a Server Component page module to a streaming Response.
 *
 * @param {object} entry       dispatch-table entry with load, loadLayouts, loadClientManifest, ppr
 * @param {Request} request
 * @param {Record<string, any>} env
 * @param {{ waitUntil: (p: Promise<any>) => void }} ctx
 * @param {{ params, searchParams }} routeCtx
 * @returns {Promise<Response>}
 */
export async function renderRSC(entry, request, env, ctx, routeCtx) {
  // B7 — PPR marker. Our renderer doesn't implement the static-shell +
  // dynamic-holes protocol; surface a clear 501 so operators know why the
  // deploy won't handle this page yet.
  if (entry.ppr) {
    return pprNotImplemented();
  }

  const url = new URL(request.url);
  const reqCtx = createRequestContext(request, env, url, routeCtx.params, ctx);

  return runWithContext(reqCtx, async () => {
    const vendored = await loadVendored();
    if (!vendored) {
      return vendorMissingResponse();
    }

    // Load module + layouts + client manifest in parallel.
    const loadLayouts = Array.isArray(entry.loadLayouts) ? entry.loadLayouts : [];
    const [pageModule, clientManifest, ...layoutModules] = await Promise.all([
      entry.load(),
      entry.loadClientManifest ? entry.loadClientManifest() : Promise.resolve(null),
      ...loadLayouts.map((fn) => fn()),
    ]);

    const PageComponent = resolveComponent(pageModule);
    if (typeof PageComponent !== "function") {
      return new Response(
        "nextcompile: page module did not export a Component. Received keys: " +
          Object.keys(pageModule).join(", "),
        { status: 500 },
      );
    }

    // Build the rendering tree — layouts wrap the page, root-first.
    const treeProps = {
      params: reqCtx.params,
      searchParams: Object.fromEntries(url.searchParams),
    };
    let tree;
    try {
      tree = await buildLayoutTree(layoutModules, PageComponent, treeProps);
    } catch (err) {
      return new Response(
        "nextcompile: layout composition failed:\n" + (err?.stack || String(err)),
        { status: 500 },
      );
    }

    // Pull the bundlerConfig out of the client manifest. Next emits it as
    // `.default.clientModules` for bundler consumption; some minors use
    // top-level `.clientModules`. Try both.
    const bundlerConfig =
      (clientManifest &&
        (clientManifest.default?.clientModules || clientManifest.clientModules)) ||
      {};

    let flightStream;
    try {
      flightStream = await vendored.renderToReadableStream(tree, bundlerConfig);
    } catch (err) {
      return new Response(
        "nextcompile RSC render failure:\n" + (err?.stack || String(err)),
        {
          status: 500,
          headers: { "content-type": "text/plain; charset=utf-8" },
        },
      );
    }

    // B3 — wrap the Flight stream in an HTML shell. Browsers need something
    // they can actually render at first paint. The shell is deliberately
    // minimal; it will grow to match Next's full shell shape as the RSC
    // renderer stabilizes.
    const htmlStream = wrapFlightInHtmlShell(flightStream, reqCtx);

    const respHeaders = new Headers(reqCtx.responseHeaders);
    respHeaders.set("content-type", "text/html; charset=utf-8");
    if (!respHeaders.has("cache-control")) {
      respHeaders.set("cache-control", "private, no-cache, no-store, must-revalidate");
    }
    for (const cookie of reqCtx.setCookies) {
      respHeaders.append("set-cookie", cookie);
    }

    return new Response(htmlStream, { status: 200, headers: respHeaders });
  });
}

/**
 * Pick the React component out of a compiled Next page module. Next
 * commonly puts it on default; some legacy emits use `Page`.
 */
function resolveComponent(mod) {
  return mod?.default || mod?.Page || mod?.Component;
}

/**
 * Compose layouts around the page. Layouts are applied root → leaf so that
 * the root layout is outermost in the final tree.
 *
 * Each compiled layout exports a component that takes { children }. We
 * invoke them as functions (not JSX) because this runtime doesn't depend
 * on JSX syntax at module top level.
 */
async function buildLayoutTree(layoutModules, PageComponent, props) {
  let node = PageComponent(props);
  // Apply layouts innermost first — because our `layoutModules` array is
  // root → leaf, we iterate in reverse so the root wraps everything.
  for (let i = layoutModules.length - 1; i >= 0; i--) {
    const LayoutComponent = resolveComponent(layoutModules[i]);
    if (typeof LayoutComponent !== "function") continue;
    const wrapped = LayoutComponent({ ...props, children: node });
    node = wrapped;
  }
  return node;
}

/**
 * Minimal HTML shell around the Flight stream. Enough to give a browser
 * something to render. The shell does three things:
 *   1. Sets the doctype + charset so encoding is unambiguous.
 *   2. Emits a placeholder <div id="__next"> for hydration targets.
 *   3. Inlines the Flight payload inside <script id="__FLIGHT_DATA__">
 *      so a future hydration bootstrap can consume it.
 *
 * Real Next clients hydrate from RSC payloads + chunk preloads that match
 * the shipped webpack chunks; we don't emit those yet. This shell at
 * least shows "the server is alive" and puts the Flight data where a
 * follow-up client bootstrap can find it.
 */
function wrapFlightInHtmlShell(flightStream, reqCtx) {
  const encoder = new TextEncoder();
  const shellPrefix = encoder.encode(
    `<!DOCTYPE html><html><head><meta charset="utf-8"><meta name="viewport" content="width=device-width, initial-scale=1"></head><body><div id="__next"></div><script id="__NEXTCOMPILE_FLIGHT__" type="application/x-nextcompile-flight">`,
  );
  const shellSuffix = encoder.encode(`</script></body></html>`);

  return new ReadableStream({
    async start(controller) {
      controller.enqueue(shellPrefix);
      const reader = flightStream.getReader();
      while (true) {
        const { value, done } = await reader.read();
        if (done) break;
        controller.enqueue(value);
      }
      controller.enqueue(shellSuffix);
      controller.close();
    },
  });
}

function vendorMissingResponse() {
  const body =
    "nextcompile: React Server Components runtime not vendored.\n\n" +
    "This deployment contains App Router pages that use Server Components,\n" +
    "but the build did not include the vendored React server bundle at:\n" +
    "  _nextdeploy/runtime/vendor/react-server-dom-webpack/server.edge.mjs\n\n" +
    "Fix: invoke nextcompile's adapter build step with RSC vendoring enabled,\n" +
    "or ship the matching react-server-dom-webpack bundle for the detected\n" +
    "React version. See runtime/vendor/README.md for the exact steps.\n\n" +
    "Load error: " + String(vendoredLoadError);
  return new Response(body, {
    status: 501,
    headers: {
      "content-type": "text/plain; charset=utf-8",
      "cache-control": "no-store",
    },
  });
}

function pprNotImplemented() {
  const body =
    "nextcompile: Partial Prerendering (PPR) is not yet implemented.\n\n" +
    "This page is marked PPR in the compiled output, which means Next\n" +
    "expects the server to stream a static shell with dynamic holes\n" +
    "resolved inline. nextcompile's RSC renderer handles fully-dynamic\n" +
    "pages today; PPR support is tracked as a follow-up milestone.\n\n" +
    "Workaround: remove `experimental.ppr` or `experimental_ppr` from\n" +
    "this route's config and redeploy.";
  return new Response(body, {
    status: 501,
    headers: {
      "content-type": "text/plain; charset=utf-8",
      "cache-control": "no-store",
    },
  });
}
