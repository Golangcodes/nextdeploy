package nextcompile

import (
	"context"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"

	"golang.org/x/sync/errgroup"
)

// scanConcurrency caps parallel file reads. Standalone trees for large apps
// can contain 50k+ files; uncapped errgroup Go routines thrash open-file
// limits on macOS (default 256). 32 is empirically a sweet spot across
// common dev hardware.
const scanConcurrency = 32

// Compiled-path prefixes that classify where a module lives in Next's
// standalone output. `app/` for App Router, `pages/` for Pages Router.
const (
	appRouterPrefix   = "app/"
	pagesRouterPrefix = "pages/"
)

// ScanCompiledServer walks the server subtree of a Next standalone build
// in parallel and returns a ModuleRef per compiled route/handler/middleware.
// Non-server assets (client chunks, static files) are skipped — those flow
// to the CDN via packaging.S3Assets, not into the Worker bundle.
//
// The caller's Payload supplies route classification; the scanner's job is
// to attach compiled file paths to each classified route and extract the
// static-analysis facts (env refs, fetch targets, RSC markers) that later
// phases consume.
func ScanCompiledServer(ctx context.Context, standaloneDir string, payload Payload) ([]ModuleRef, error) {
	serverDir := filepath.Join(standaloneDir, payloadDistDir(payload), "server")
	if _, err := os.Stat(serverDir); err != nil {
		return nil, fmt.Errorf("server dir not found at %s: %w", serverDir, err)
	}

	paths, err := collectCompiledFiles(serverDir)
	if err != nil {
		return nil, err
	}

	g, gctx := errgroup.WithContext(ctx)
	g.SetLimit(scanConcurrency)

	// classifyRoot is the parent of `server/` — i.e. the .next directory.
	// Paths relative to classifyRoot start with "server/..." which is what
	// routePathFromCompiled / kindFromCompiled expect.
	classifyRoot := filepath.Dir(serverDir)

	refs := make([]ModuleRef, len(paths))
	for i, p := range paths {
		g.Go(func() error {
			if err := gctx.Err(); err != nil {
				return err
			}
			ref, err := analyzeModule(standaloneDir, classifyRoot, p)
			if err != nil {
				return fmt.Errorf("analyze %s: %w", p, err)
			}
			refs[i] = ref
			return nil
		})
	}
	if err := g.Wait(); err != nil {
		return nil, err
	}

	// Tag each ref against the route classification. This is the bit that
	// distinguishes an /api/users handler from a /dashboard page — the
	// compiled path alone is ambiguous (both land under server/app/...).
	refs = classifyRefs(refs, payload)

	// Second pass: attach client reference manifests + layout chains to
	// page refs. Cheap — each is a stat(). Done after classification so we
	// only walk up the tree for confirmed page kinds.
	refs = attachClientManifests(refs, standaloneDir, classifyRoot)
	refs = attachLayoutChains(refs, standaloneDir, classifyRoot)

	sort.Slice(refs, func(i, j int) bool {
		return refs[i].RoutePath < refs[j].RoutePath
	})
	return refs, nil
}

// attachClientManifests looks for the Next-emitted
// `page_client-reference-manifest.json` sibling next to each page.js and
// attaches its relative path to the ModuleRef. Next 14+ emits one per RSC
// page; its contents are the bundlerConfig the Flight encoder needs to
// serialize "use client" boundaries.
func attachClientManifests(refs []ModuleRef, standaloneDir, classifyRoot string) []ModuleRef {
	for i := range refs {
		r := &refs[i]
		if r.Kind != RouteKindPage {
			continue
		}
		// Sibling convention: for .../<route>/page.js, look for
		// .../<route>/page_client-reference-manifest.json.
		abs := filepath.Join(standaloneDir, r.CompiledPath)
		dir := filepath.Dir(abs)
		base := filepath.Base(abs)
		stem := strings.TrimSuffix(base, filepath.Ext(base))
		candidate := filepath.Join(dir, stem+"_client-reference-manifest.json")
		if _, err := os.Stat(candidate); err != nil {
			continue
		}
		rel, err := filepath.Rel(standaloneDir, candidate)
		if err != nil {
			continue
		}
		r.ClientManifestPath = filepath.ToSlash(rel)
	}
	return refs
}

// attachLayoutChains walks the App Router tree for each page ref and
// records every layout.js on the path from root to the page's containing
// directory. classifyRoot lets us compute directory structure without
// re-parsing the standalone tree.
func attachLayoutChains(refs []ModuleRef, standaloneDir, classifyRoot string) []ModuleRef {
	// Index layouts by their enclosing directory (relative to classifyRoot).
	// A layout at `server/app/dashboard/layout.js` applies to everything
	// under `server/app/dashboard/**`.
	layoutByDir := map[string]string{}
	for _, r := range refs {
		if r.Kind != RouteKindLayout {
			continue
		}
		// r.CompiledPath is relative to standaloneDir; we want it relative
		// to classifyRoot for comparison with page dirs.
		layoutAbs := filepath.Join(standaloneDir, r.CompiledPath)
		classifyRel, err := filepath.Rel(classifyRoot, layoutAbs)
		if err != nil {
			continue
		}
		dir := filepath.Dir(filepath.ToSlash(classifyRel))
		layoutByDir[dir] = r.CompiledPath
	}

	for i := range refs {
		r := &refs[i]
		if r.Kind != RouteKindPage {
			continue
		}
		pageAbs := filepath.Join(standaloneDir, r.CompiledPath)
		pageRel, err := filepath.Rel(classifyRoot, pageAbs)
		if err != nil {
			continue
		}
		pageDir := filepath.Dir(filepath.ToSlash(pageRel))

		// Walk from the page's directory back up to the root, collecting
		// every layout.js sitting in an ancestor directory (including
		// server/app itself). Result is ordered page-nearest to
		// root-nearest; we reverse below so the runtime applies root-first.
		//
		// The loop is authoritative: every ancestor gets checked exactly
		// once, so a page under server/app picks up the root layout
		// without any special casing.
		var chain []string
		for dir := pageDir; dir != "." && dir != "/"; dir = filepath.Dir(dir) {
			if layoutPath, ok := layoutByDir[dir]; ok {
				chain = append(chain, layoutPath)
			}
		}

		// Reverse to root→leaf ordering.
		for l, rr := 0, len(chain)-1; l < rr; l, rr = l+1, rr-1 {
			chain[l], chain[rr] = chain[rr], chain[l]
		}
		r.LayoutChain = chain
	}
	return refs
}

// payloadDistDir returns the configured dist directory or the Next default.
func payloadDistDir(p Payload) string {
	if p.DistDir != "" {
		return p.DistDir
	}
	return ".next"
}

// collectCompiledFiles walks serverDir and returns every .js / .mjs file
// below it. Ordered deterministically for reproducible builds.
func collectCompiledFiles(serverDir string) ([]string, error) {
	var out []string
	err := filepath.WalkDir(serverDir, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			// Skip Next's own internal chunks — they're required by the
			// compiled modules but not directly dispatchable. esbuild
			// pulls them in transitively when bundling the ModuleRef set.
			base := d.Name()
			if base == "chunks" || base == "pages-manifest" {
				return fs.SkipDir
			}
			return nil
		}
		ext := filepath.Ext(path)
		if ext == ".js" || ext == ".mjs" {
			out = append(out, path)
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	sort.Strings(out)
	return out, nil
}

// analyzeModule reads a single compiled file and extracts the facts the
// compiler needs for dispatch + binding derivation. It does NOT parse JS —
// Next's compiled output is regular enough that regex grep on the source
// is accurate and orders of magnitude faster than a full AST pass. If
// that assumption ever breaks we'll swap in esbuild's parser here.
//
// standaloneDir is the bundle root (used for CompiledPath display).
// classifyRoot is the directory whose children are `server/`, `static/`,
// etc. — normally the Next dist dir. Paths relative to classifyRoot are
// what routePathFromCompiled / kindFromCompiled are calibrated for.
func analyzeModule(standaloneDir, classifyRoot, absPath string) (ModuleRef, error) {
	displayRel, err := filepath.Rel(standaloneDir, absPath)
	if err != nil {
		return ModuleRef{}, err
	}
	classifyRel, err := filepath.Rel(classifyRoot, absPath)
	if err != nil {
		return ModuleRef{}, err
	}
	info, err := os.Stat(absPath)
	if err != nil {
		return ModuleRef{}, err
	}

	data, err := os.ReadFile(absPath) // #nosec G304 — compiler reads its own output tree
	if err != nil {
		return ModuleRef{}, err
	}
	src := string(data)

	classifySlash := filepath.ToSlash(classifyRel)
	ref := ModuleRef{
		CompiledPath: filepath.ToSlash(displayRel),
		RoutePath:    routePathFromCompiled(classifySlash),
		Kind:         kindFromCompiled(classifySlash),
		ByteSize:     info.Size(),
		EnvRefs:      uniqueStrings(envRefPattern.FindAllStringSubmatch(src, -1), 1),
		FetchTargets: uniqueStrings(fetchURLPattern.FindAllStringSubmatch(src, -1), 1),
		UsesRSC:      usesRSCPattern.MatchString(src),
		HasActions:   hasActionsPattern.MatchString(src),
		PPREnabled:   pprPattern.MatchString(src),
	}
	return ref, nil
}

// ── Regex surface ────────────────────────────────────────────────────────────
//
// These are tuned against Next 13/14/15 compiled output. Patterns are
// conservative — false negatives (missing a ref) are cheaper than false
// positives (suggesting a binding that doesn't exist). When a new Next
// version lands, the CI fixture matrix (testdata/fixtures/*) will flag
// any pattern that drifts.

var (
	// process.env.FOO_BAR — minified output preserves the exact form because
	// Next explicitly doesn't rename env references (they're static-replaced
	// by webpack/turbopack in some paths, left intact in others).
	envRefPattern = regexp.MustCompile(`process\.env\.([A-Z][A-Z0-9_]*)`)

	// fetch("https://...") or fetch('https://...') — double OR single quote,
	// url-ish body up to the next quote. Template literals are intentionally
	// not matched here; they'd produce too many false positives.
	fetchURLPattern = regexp.MustCompile(`\bfetch\(\s*['"]([a-z][a-z0-9+.-]*://[^'"]+)['"]`)

	// Flight / RSC markers. Two tells:
	//   - "use client" pragma preserved in compiled output
	//   - imports from react-server-dom-* runtime modules
	usesRSCPattern = regexp.MustCompile(`"use client"|react-server-dom-`)

	// Server Actions: compiled modules tag their action exports with a
	// specific comment marker plus a $$typeof reference. We look for either.
	hasActionsPattern = regexp.MustCompile(`"use server"|__next_internal_action_entry_do_not_use__`)

	// PPR opt-in markers. Next 14+ exports `experimental_ppr = true` from
	// pages that opt into Partial Prerendering. Compiled output preserves
	// the identifier. `__NEXT_PPR_STATIC_` is a second tell observed in
	// some Next 15 canary builds where the static shell has already been
	// materialized at build time.
	pprPattern = regexp.MustCompile(`experimental_ppr\s*=\s*true|__NEXT_PPR_STATIC_`)
)

// uniqueStrings deduplicates a regex FindAllStringSubmatch result by the
// capture group at `group` (1 = first paren group). Sorted for determinism.
func uniqueStrings(matches [][]string, group int) []string {
	if len(matches) == 0 {
		return nil
	}
	seen := make(map[string]struct{}, len(matches))
	for _, m := range matches {
		if len(m) <= group {
			continue
		}
		seen[m[group]] = struct{}{}
	}
	out := make([]string, 0, len(seen))
	for s := range seen {
		out = append(out, s)
	}
	sort.Strings(out)
	return out
}

// ── Path classification ─────────────────────────────────────────────────────

// routePathFromCompiled translates a compiled path into the URL route.
// Examples:
//
//	server/app/dashboard/page.js       → /dashboard
//	server/app/api/users/route.js      → /api/users
//	server/pages/index.js              → /
//	server/pages/blog/[slug].js        → /blog/[slug]
//	server/middleware.js               → /_middleware
//	server/proxy.js                    → /_proxy
//	server/app/layout.js               → /_root_layout
func routePathFromCompiled(rel string) string {
	rel = strings.TrimPrefix(rel, "server/")

	switch {
	case rel == "middleware.js" || rel == "middleware.mjs":
		return "/_middleware"
	case rel == "proxy.js" || rel == "proxy.mjs":
		return "/_proxy"
	case strings.HasPrefix(rel, appRouterPrefix):
		return appRouteFromPath(strings.TrimPrefix(rel, appRouterPrefix))
	case strings.HasPrefix(rel, pagesRouterPrefix):
		return pagesRouteFromPath(strings.TrimPrefix(rel, pagesRouterPrefix))
	}
	return ""
}

// appRouteFromPath handles App Router conventions. page.js / route.js /
// layout.js are all nested under directories that name the route segment.
// Route groups like (marketing) are stripped. Intercepting routes
// ((..)) are preserved verbatim — the dispatcher resolves them.
func appRouteFromPath(rel string) string {
	rel = strings.TrimSuffix(rel, filepath.Ext(rel))
	base := filepath.Base(rel)
	dir := filepath.Dir(rel)

	// Special files that don't contribute their filename to the URL.
	switch base {
	case "page", "route":
		// fall through — directory is the URL
	case "layout":
		if dir == "." {
			return "/_root_layout"
		}
		return "/" + stripRouteGroups(dir) + "/_layout"
	default:
		// Other compiled files (e.g. not-found.js, loading.js) — tag by name.
		return "/" + stripRouteGroups(dir) + "/_" + base
	}

	if dir == "." {
		return "/"
	}
	return "/" + stripRouteGroups(dir)
}

// pagesRouteFromPath handles the older Pages Router. index.js collapses to /,
// other files become their path. API routes under pages/api/ are flagged by
// kindFromCompiled, not here.
func pagesRouteFromPath(rel string) string {
	rel = strings.TrimSuffix(rel, filepath.Ext(rel))
	if rel == "index" {
		return "/"
	}
	if strings.HasSuffix(rel, "/index") {
		rel = strings.TrimSuffix(rel, "/index")
	}
	return "/" + rel
}

// stripRouteGroups removes Next.js route group segments like (marketing)
// from a path. Route groups are directory-only organization; they never
// appear in URLs.
func stripRouteGroups(p string) string {
	parts := strings.Split(p, "/")
	out := parts[:0]
	for _, part := range parts {
		if strings.HasPrefix(part, "(") && strings.HasSuffix(part, ")") {
			continue
		}
		out = append(out, part)
	}
	return strings.Join(out, "/")
}

// kindFromCompiled classifies a compiled path without reading the source.
// The source-based checks (UsesRSC, HasActions) refine Kind later in
// classifyRefs — this function just gets us to a reasonable default.
func kindFromCompiled(rel string) RouteKind {
	rel = strings.TrimPrefix(rel, "server/")

	switch {
	case rel == "middleware.js" || rel == "middleware.mjs":
		return RouteKindMiddleware
	case rel == "proxy.js" || rel == "proxy.mjs":
		return RouteKindProxy
	case strings.HasSuffix(rel, "/route.js") || strings.HasSuffix(rel, "/route.mjs"):
		return RouteKindAPI
	case strings.HasPrefix(rel, "pages/api/"):
		return RouteKindAPI
	case strings.HasSuffix(rel, "/page.js") || strings.HasSuffix(rel, "/page.mjs"):
		return RouteKindPage
	case strings.HasSuffix(rel, "/layout.js") || strings.HasSuffix(rel, "/layout.mjs"):
		return RouteKindLayout
	case strings.HasPrefix(rel, pagesRouterPrefix):
		return RouteKindPage
	}
	return RouteKindUnknown
}

// classifyRefs refines Kind against the Payload.Routes classification.
// The compiled-path heuristic in kindFromCompiled gets the common cases
// (middleware.js, route.js, page.js); the manifest overrides only for
// edge cases like a custom API route declared outside server/api/.
//
// Intentionally not consulted here:
//   - RouteInfo.MiddlewareRoutes — this is "paths middleware APPLIES TO",
//     not "modules that ARE middleware". Mixing them reclassifies pages.
//   - HasActions — action promotion is confirmed by actions.go against
//     Next's server-reference-manifest, not the path shape alone.
func classifyRefs(refs []ModuleRef, payload Payload) []ModuleRef {
	apiSet := sliceToSet(payload.Routes.APIRoutes)

	for i := range refs {
		r := &refs[i]
		// File-name heuristics already identified these special modules;
		// don't let a RoutePath coincidence reclassify them.
		if r.Kind == RouteKindMiddleware || r.Kind == RouteKindProxy {
			continue
		}
		if _, ok := apiSet[r.RoutePath]; ok {
			r.Kind = RouteKindAPI
		}
	}
	return refs
}

func sliceToSet(ss []string) map[string]struct{} {
	out := make(map[string]struct{}, len(ss))
	for _, s := range ss {
		out[s] = struct{}{}
	}
	return out
}
