// Error responses. Kept deliberately tiny so they're always reachable
// even when downstream modules throw at module-init time.
//
// Phase 2 will honor Next's app/not-found.js / app/error.js — for now,
// we return plain text responses so operators can grep logs.

export function notFound() {
  return new Response("Not Found", {
    status: 404,
    headers: { "content-type": "text/plain; charset=utf-8" },
  });
}

export function serverError(err) {
  const message = err?.stack ? err.stack : String(err);
  return new Response("Internal Server Error\n\n" + message, {
    status: 500,
    headers: {
      "content-type": "text/plain; charset=utf-8",
      // Never cache errors — stale 500s are worse than recomputing them.
      "cache-control": "no-store",
    },
  });
}
