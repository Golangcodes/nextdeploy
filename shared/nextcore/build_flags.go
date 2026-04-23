package nextcore

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

// MajorVersion parses a semver-ish string like "16.1.6", "^16.0.0", "~15.2" and
// returns the major component. Returns 0 if it cannot be parsed (which the
// callers treat as "don't apply Next 16-specific behaviour").
func MajorVersion(v string) int {
	v = strings.TrimSpace(v)
	v = strings.TrimLeft(v, "^~>=<v ")
	if v == "" {
		return 0
	}
	parts := strings.SplitN(v, ".", 2)
	n, err := strconv.Atoi(parts[0])
	if err != nil {
		return 0
	}
	return n
}

// needsWebpackFlag decides whether we should force `next build --webpack`.
// Starting with Next 16 Turbopack is the default builder; a project that ships
// a `webpack:` config without a matching `turbopack:` key fails the build
// (Turbopack refuses to proceed). Passing --webpack makes Next fall back to
// the webpack builder and honour the existing config.
func needsWebpackFlag(cfg *NextConfig, nextVersion string) bool {
	if cfg == nil || cfg.Webpack == nil {
		return false
	}
	if cfg.Turbopack != nil {
		return false
	}
	return MajorVersion(nextVersion) >= 16
}

// scriptEndsWithNextBuild reports whether the final command in an npm-style
// build script is a `next build` invocation. Used to decide if appending
// `-- --webpack` via the package-manager argv forwarding will land on next.
//
// Recognises: `next build`, `next-build`, `npx next build`, `bunx next build`,
// and yarn's `yarn next build`. Flags after `next build` (like `--profile`)
// are fine — argv forwarding appends to the tail of the script string.
func scriptEndsWithNextBuild(script string) bool {
	last := lastCommandInScript(script)
	last = strings.TrimSpace(last)
	if last == "" {
		return false
	}
	candidates := []string{
		"next build",
		"next-build",
		"npx next build",
		"npx --no-install next build",
		"bunx next build",
		"yarn next build",
		"pnpm exec next build",
	}
	for _, c := range candidates {
		if strings.HasPrefix(last, c) {
			return true
		}
	}
	return false
}

// lastCommandInScript returns the trailing command of a shell-style chain,
// splitting on the highest-precedence separators we care about (&&, ||, ;).
// Pipes (|) are deliberately ignored — piping into `next build` is not a
// thing we want to reason about.
func lastCommandInScript(script string) string {
	seps := []string{"&&", "||", ";"}
	tail := script
	changed := true
	for changed {
		changed = false
		for _, sep := range seps {
			if i := strings.LastIndex(tail, sep); i >= 0 {
				tail = tail[i+len(sep):]
				changed = true
			}
		}
	}
	return tail
}

// scriptAlreadyPicksBuilder returns true if the user's build script already
// explicitly asks for a builder (either --webpack or --turbopack). In that
// case we leave it alone.
func scriptAlreadyPicksBuilder(script string) bool {
	return strings.Contains(script, "--webpack") || strings.Contains(script, "--turbopack")
}

// readBuildScriptBody returns the raw string of the "build" entry under
// "scripts" in package.json. Empty string on any error (callers treat that as
// "can't reason about the script, skip injection").
func readBuildScriptBody(projectDir string) string {
	// #nosec G304
	data, err := os.ReadFile(filepath.Join(projectDir, "package.json"))
	if err != nil {
		return ""
	}
	var pkg PackageJSON
	if err := json.Unmarshal(data, &pkg); err != nil {
		return ""
	}
	return pkg.Scripts["build"]
}

// buildFlagLogger is a minimal subset of the project logger surface — kept
// narrow so this file stays testable without pulling the whole logger.
type buildFlagLogger interface {
	Info(format string, args ...any)
	Warn(format string, args ...any)
}

// MaybeInjectWebpackFlag conditionally appends `-- --webpack` to buildCmd so
// the underlying `next build` runs under the webpack builder. Returns the
// (possibly unchanged) command.
//
// Decision table (only modifies when all four are true):
//   - next.config has a `webpack:` key
//   - next.config has no `turbopack:` key
//   - Next.js major version >= 16
//   - package.json scripts.build ends with a `next build` invocation and
//     does not already pin --webpack / --turbopack
//
// If the first three are true but the script is too exotic to patch (e.g.
// `next build && post-step`), we log a clear warning and leave the command
// untouched so the user sees the real Turbopack error.
func MaybeInjectWebpackFlag(
	buildCmd, projectDir string,
	cfg *NextConfig,
	nextVersion string,
	log buildFlagLogger,
) string {
	if !needsWebpackFlag(cfg, nextVersion) {
		return buildCmd
	}

	script := readBuildScriptBody(projectDir)
	if script == "" {
		if log != nil {
			log.Warn("Detected Next >=16 with webpack config but no turbopack key, and could not read scripts.build from package.json — leaving build command unchanged. Add `turbopack: {}` to next.config or run `next build --webpack` manually.")
		}
		return buildCmd
	}

	if scriptAlreadyPicksBuilder(script) {
		return buildCmd
	}

	if !scriptEndsWithNextBuild(script) {
		if log != nil {
			log.Warn("Detected Next >=16 with webpack config but no turbopack key. Your `build` script doesn't end with a plain `next build` invocation (got: %q), so NextDeploy cannot safely inject --webpack. The build will likely fail under Turbopack — either add `turbopack: {}` to next.config or pin `--webpack` in your build script.", script)
		}
		return buildCmd
	}

	if log != nil {
		log.Info("Next >=16 detected with webpack config but no turbopack key — appending `-- --webpack` to build command so Turbopack is bypassed.")
	}
	return buildCmd + " -- --webpack"
}
