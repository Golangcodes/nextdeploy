package cmd

var buildExplanation = explanation{
	Name:     "build",
	Synopsis: "Build the Next.js app and prepare a deployable tarball.",
	Summary: "`build` runs the Next.js build (via nextcore), validates the " +
		"resulting output mode against the declared target (serverless " +
		"vs vps), assembles a release directory, and produces app.tar.gz " +
		"— the artifact `ship` uploads. Git-aware: re-running without " +
		"code changes is a no-op unless --force is passed.",
	Phases: []phase{
		{
			Num:       1,
			Title:     "Incremental build gate",
			Narrative: "Checks .nextdeploy/build.lock against the current git commit. If unchanged, exits 0 immediately. --force bypasses this gate.",
			Ref:       "cli/cmd/build.go:24",
			Function:  "nextcore.ValidateBuildState",
			Output:    "skip (exit 0) or proceed",
		},
		{
			Num:       2,
			Title:     "Generate metadata",
			Narrative: "Runs the Next.js build, parses next.config + all .next/ manifests, produces .nextdeploy/metadata.json (the NextCorePayload the rest of the toolchain consumes).",
			Ref:       "cli/cmd/build.go:33",
			Function:  "nextcore.GenerateMetadata",
			Input:     "next.config.{js,mjs}, .next/",
			Output:    ".nextdeploy/metadata.json + NextCorePayload",
		},
		{
			Num:       3,
			Title:     "Pre-build validations",
			Narrative: "Enforces output-mode requirements. Serverless requires 'output: standalone'; Server Actions forbid 'output: export'. Exits non-zero with clear guidance on mismatch.",
			Ref:       "cli/cmd/build.go:44",
			Output:    "fatal error on mismatch, warning on suboptimal choice",
		},
		{
			Num:       4,
			Title:     "Assemble release directory",
			Narrative: "Standalone: copies public/ and .next/static/ into .next/standalone/, plus metadata.json. Export: uses the export dir directly. Default: metadata at repo root.",
			Ref:       "cli/cmd/build.go:59",
			Function:  "utils.CopyDir + utils.CopyFile",
			Output:    "releaseDir populated with the full deploy tree",
		},
		{
			Num:       5,
			Title:     "Create tarball",
			Narrative: "Streams releaseDir into app.tar.gz. Content filtering is target-aware (serverless strips dev-only files; vps keeps more).",
			Ref:       "cli/cmd/build.go:98",
			Function:  "utils.CreateTarball",
			Input:     "releaseDir + TargetType",
			Output:    "app.tar.gz at repo root",
		},
		{
			Num:       6,
			Title:     "Post-build audit",
			Narrative: "Serverless only: measures final bundle size and node_modules overhead. Warns at 200MB; hard warning at 250MB (Lambda's unzipped limit).",
			Ref:       "cli/cmd/build.go:106",
			Function:  "packaging.AuditStandaloneSize",
			Output:    "log summary + size warnings",
			Notes:     []string{"Use `nextdeploy inspect` for the full offender breakdown."},
		},
	},
}

func init() {
	registerExplain(buildCmd, &buildExplanation)
}
