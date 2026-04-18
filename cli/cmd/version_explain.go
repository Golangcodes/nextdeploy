package cmd

var versionExplanation = explanation{
	Name:     "version",
	Synopsis: "Print the nextdeploy CLI version.",
	Summary: "`version` prints the semver baked into this binary plus the " +
		"build commit when available. No network access. Used by scripts " +
		"that want to gate on a minimum version.",
	Phases: []phase{
		{
			Num:       1,
			Title:     "Read baked-in version",
			Narrative: "Version string is set by the build via -ldflags -X main.version=…. Falls back to 'dev' for local builds without the ldflag.",
			Ref:       "shared/version.go",
			Function:  "shared.Version",
			Output:    "semver string",
		},
		{
			Num:       2,
			Title:     "Print",
			Narrative: "Writes the version (+ optional commit hash) to stdout. No errors; exit 0 always.",
			Ref:       "cli/cmd/version.go:14",
		},
	},
}

func init() {
	registerExplain(versionCmd, &versionExplanation)
}
