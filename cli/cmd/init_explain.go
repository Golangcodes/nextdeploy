package cmd

var initExplanation = explanation{
	Name:     "init",
	Synopsis: "Scaffold a minimal nextdeploy.yml for the current Next.js project.",
	Summary: "`init` creates the nextdeploy.yml config file at the repo root " +
		"with sensible defaults. It inspects package.json to seed app " +
		"name + version and asks the operator a few questions about " +
		"target type (serverless vs vps) and provider (AWS / Cloudflare). " +
		"Re-running init on an existing config is a no-op — edit " +
		"nextdeploy.yml directly after first creation.",
	Phases: []phase{
		{
			Num:       1,
			Title:     "Detect existing config",
			Narrative: "If nextdeploy.yml already exists, init exits with a message rather than clobbering. Operators opt into regen by deleting the file first.",
			Ref:       "cli/cmd/init.go",
			Output:    "skip if nextdeploy.yml present",
		},
		{
			Num:       2,
			Title:     "Interactive prompts",
			Narrative: "Asks: target type (serverless|vps), provider (aws|cloudflare) if serverless, app domain (optional). Pre-fills answers from package.json name/version when available.",
			Ref:       "cli/cmd/init.go",
			Output:    "answers accumulated in memory",
		},
		{
			Num:       3,
			Title:     "Write nextdeploy.yml",
			Narrative: "Renders a commented template reflecting the operator's choices and writes to the repo root with 0644 perms. Template includes pointers to the config reference and common next-steps (init → secrets set → build → ship).",
			Ref:       "cli/cmd/init.go",
			Output:    "nextdeploy.yml at repo root",
		},
	},
}

func init() {
	registerExplain(initCmd, &initExplanation)
}
