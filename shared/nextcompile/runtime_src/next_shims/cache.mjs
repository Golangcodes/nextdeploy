// Shim target for `import "next/cache"`. The adapter's esbuild config
// rewrites that specifier to this file, so user code that imports
// `revalidatePath` / `revalidateTag` / `unstable_cache` resolves to our
// runtime without source modifications.
export {
  revalidatePath,
  revalidateTag,
  unstable_cache,
} from "../cache.mjs";
