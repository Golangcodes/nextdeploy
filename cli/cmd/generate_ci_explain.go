package cmd

var generateCIExplanation = explanation{
	Name:     "generate-ci",
	Synopsis: "Emit a GitHub Actions workflow that runs build + ship on push.",
	Summary: "`generate-ci` writes a minimal, opinionated workflow file to " +
		".github/workflows/nextdeploy.yml. The workflow triggers on push " +
		"to main, installs Node + pnpm, runs nextdeploy build, then ships. " +
		"Secrets are read from GitHub Actions env — the workflow " +
		"references them with the same names the credstore expects " +
		"(AWS_ACCESS_KEY_ID / CLOUDFLARE_API_TOKEN / etc.), so no " +
		"bespoke mapping is needed.",
	Phases: []phase{
		{
			Num:       1,
			Title:     "Detect existing workflow",
			Narrative: "Checks for .github/workflows/nextdeploy.yml. Existing file → warn + exit; overwrite only with --force to prevent silent CI config loss.",
			Ref:       "cli/cmd/generate_ci.go",
			Output:    "skip or proceed",
		},
		{
			Num:       2,
			Title:     "Read config for target",
			Narrative: "Loads nextdeploy.yml to pick the right template: serverless workflows skip the server-ssh steps; VPS workflows include them.",
			Ref:       "cli/cmd/generate_ci.go",
			Function:  "config.Load",
		},
		{
			Num:       3,
			Title:     "Render + write workflow",
			Narrative: "Renders the embedded template with {AppName, Provider, NodeVersion} substituted. Writes to .github/workflows/nextdeploy.yml with 0644 perms.",
			Ref:       "cli/cmd/generate_ci.go",
			Output:    ".github/workflows/nextdeploy.yml",
		},
		{
			Num:       4,
			Title:     "Print follow-up",
			Narrative: "Echoes the exact GitHub Secrets the user still needs to set in their repo (provider API token, SSH key for VPS, etc.). CI runs fail loudly until those are populated — by design.",
			Ref:       "cli/cmd/generate_ci.go",
			Output:    "stdout instructions",
		},
	},
}

func init() {
	registerExplain(generateCICmd, &generateCIExplanation)
}
