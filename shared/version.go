package shared

const Version = "v0.7.18"

// Commit is the short git commit the binary was built from. Populated via
// ldflags at build time (see Makefile / magefile.go / .goreleaser.yml).
// Left as a var rather than const so -X can rewrite it.
var Commit = "unknown"
