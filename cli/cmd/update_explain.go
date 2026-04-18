package cmd

var updateExplanation = explanation{
	Name:     "update",
	Synopsis: "Check for and install CLI updates.",
	Summary: "`update` is the self-update path for the nextdeploy binary. It " +
		"queries the latest release, compares against the running " +
		"version, and atomically replaces the binary on disk when a " +
		"newer one is available. Safe to run inside CI — no-op when " +
		"already current.",
	Phases: []phase{
		{
			Num:       1,
			Title:     "Resolve latest version",
			Narrative: "Fetches the release feed from the configured update endpoint. Compares against the baked-in binary version via semver.",
			Ref:       "shared/updater/updater.go",
			Function:  "updater.CheckLatest",
			Output:    "latestVersion, downloadURL",
		},
		{
			Num:       2,
			Title:     "Skip if current",
			Narrative: "Current == latest → exit 0 with a friendly 'already current' line. Most CI invocations land here.",
			Output:    "skip if up-to-date",
		},
		{
			Num:       3,
			Title:     "Download + verify",
			Narrative: "Downloads the release tarball to a temp file, verifies the checksum (and signature if configured), unpacks the new binary. G110 decompression-bomb protection applies.",
			Ref:       "shared/updater/updater.go",
			Function:  "updater.SelfUpdateWithOptions",
			Output:    "tempfile with new binary",
		},
		{
			Num:       4,
			Title:     "Atomic replace",
			Narrative: "Renames the temp file over the currently-executing binary. On Linux/macOS the old inode survives the rename so the running process keeps working; subsequent invocations pick up the new version.",
			Ref:       "shared/updater/updater.go",
			Function:  "atomicReplace",
			Output:    "binary on PATH replaced in place",
		},
	},
}

func init() {
	registerExplain(updateCmd, &updateExplanation)
}
