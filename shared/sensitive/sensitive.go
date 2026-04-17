// Package sensitive holds a process-wide registry of secret values that must
// never appear in any log line, error message, or stdout output.
//
// Usage:
//
//	token := os.Getenv("CLOUDFLARE_API_TOKEN")
//	sensitive.Register(token)            // ← BEFORE token reaches any code path that may log
//	scrubbed := sensitive.Scrub(message) // applied automatically by the shared Logger
//
// Any value passed to Register is replaced with "***" wherever it appears in
// strings passed through Scrub. Values shorter than minRedactLen are ignored
// to avoid scrubbing common short tokens (e.g. account IDs that may legitimately
// appear in URLs).
package sensitive

import (
	"fmt"
	"io"
	"os"
	"regexp"
	"strings"
	"sync"
)

const (
	// minRedactLen is the shortest registered value we will scrub. Anything
	// shorter is too likely to cause false positives in normal log output.
	minRedactLen = 12
	// redaction is the replacement token. Short on purpose so log lines stay
	// scannable.
	redaction = "***"
)

var (
	mu         sync.RWMutex
	registered = map[string]struct{}{}
)

// Register adds value to the global redaction set. Safe for concurrent use.
// Values shorter than minRedactLen are silently ignored (avoids redacting
// short identifiers and breaking unrelated logs).
func Register(values ...string) {
	mu.Lock()
	defer mu.Unlock()
	for _, v := range values {
		if len(v) < minRedactLen {
			continue
		}
		registered[v] = struct{}{}
	}
}

// Clear empties the registry. Intended for tests.
func Clear() {
	mu.Lock()
	defer mu.Unlock()
	registered = map[string]struct{}{}
}

// commonPatterns catches well-known credential shapes even when the producing
// code forgot to call Register. Defense in depth — order matters: more specific
// patterns first.
var commonPatterns = []*regexp.Regexp{
	// Bearer tokens in HTTP-style strings
	regexp.MustCompile(`(?i)(Bearer\s+)[A-Za-z0-9._\-]{12,}`),
	// AWS access key IDs (always start with AKIA/ASIA, exactly 20 chars)
	regexp.MustCompile(`\b(AKIA|ASIA)[A-Z0-9]{16}\b`),
	// Authorization header value forms
	regexp.MustCompile(`(?i)(Authorization:\s*)[A-Za-z0-9._\-+=/]{12,}`),
}

// Scrub returns s with every registered value replaced by "***" and every
// well-known credential pattern collapsed. Cheap when registry is empty.
func Scrub(s string) string {
	mu.RLock()
	defer mu.RUnlock()

	if len(registered) == 0 && !mightContainPattern(s) {
		return s
	}

	out := s
	for v := range registered {
		if v == "" {
			continue
		}
		out = strings.ReplaceAll(out, v, redaction)
	}

	out = commonPatterns[0].ReplaceAllString(out, "${1}"+redaction)
	out = commonPatterns[1].ReplaceAllString(out, redaction)
	out = commonPatterns[2].ReplaceAllString(out, "${1}"+redaction)
	return out
}

// mightContainPattern is a fast pre-check so we skip regex work on most
// log lines. Keep these substrings in lower-case.
func mightContainPattern(s string) bool {
	lower := strings.ToLower(s)
	return strings.Contains(lower, "bearer ") ||
		strings.Contains(lower, "authorization:") ||
		strings.Contains(s, "AKIA") ||
		strings.Contains(s, "ASIA")
}

// ── Safe printers ───────────────────────────────────────────────────────────
//
// Use these wherever you'd otherwise call fmt.Print* / fmt.Fprint* with content
// derived from external systems (errors from cloud APIs, subprocess output,
// daemon responses) — anything that might inadvertently echo back a token.
// The raw value is formatted, then Scrub'd, then written.

// Print scrubs and writes to stdout. Mirrors fmt.Print signature.
func Print(a ...any) (int, error) {
	return io.WriteString(os.Stdout, Scrub(fmt.Sprint(a...)))
}

// Println scrubs and writes to stdout with a trailing newline.
func Println(a ...any) (int, error) {
	return io.WriteString(os.Stdout, Scrub(fmt.Sprintln(a...)))
}

// Printf scrubs and writes to stdout. Mirrors fmt.Printf signature.
func Printf(format string, a ...any) (int, error) {
	return io.WriteString(os.Stdout, Scrub(fmt.Sprintf(format, a...)))
}

// Fprint scrubs and writes to w. Mirrors fmt.Fprint signature.
func Fprint(w io.Writer, a ...any) (int, error) {
	return io.WriteString(w, Scrub(fmt.Sprint(a...)))
}

// Fprintln scrubs and writes to w with a trailing newline.
func Fprintln(w io.Writer, a ...any) (int, error) {
	return io.WriteString(w, Scrub(fmt.Sprintln(a...)))
}

// Fprintf scrubs and writes to w. Mirrors fmt.Fprintf signature.
func Fprintf(w io.Writer, format string, a ...any) (int, error) {
	return io.WriteString(w, Scrub(fmt.Sprintf(format, a...)))
}
