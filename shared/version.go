package shared

// Version is set at build time via -ldflags.
// Use a variable (not a constant) so the value can be overridden by -ldflags.
// Default to "dev" for local or non-release builds.
var Version = "v0.7.3"
