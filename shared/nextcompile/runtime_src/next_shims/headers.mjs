// Shim target for `import "next/headers"`. All three async accessors
// flow through nextcompile's ALS-backed context module.
export { cookies, headers, draftMode } from "../context.mjs";
