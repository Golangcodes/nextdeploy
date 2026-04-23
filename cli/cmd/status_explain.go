package cmd

var statusExplanation = explanation{
	Name:     "status",
	Synopsis: "Show the current state of the deployed app.",
	Summary: "`status` queries the configured provider for the live state of " +
		"the app: serverless surfaces the worker/function name, most " +
		"recent deployment timestamp, and health probes; VPS surfaces " +
		"the daemon's report for each configured server (running release, " +
		"process health, open ports).",
	Phases: []phase{
		{
			Num:       1,
			Title:     "Load config",
			Narrative: "Reads nextdeploy.yml to determine target type and provider.",
			Ref:       "cli/cmd/status.go",
			Function:  "config.Load",
		},
		{
			Num:       2,
			Title:     "Branch by target",
			Narrative: "Serverless: calls Provider.GetResourceMap. VPS: opens SSH sessions to each configured server and queries the nextdeploy daemon's /status endpoint.",
			Ref:       "cli/cmd/status.go",
			Function:  "Provider.GetResourceMap | daemon./status",
		},
		{
			Num:       3,
			Title:     "Render table",
			Narrative: "Prints a compact status table per host / resource. Colored by health; errors highlighted. Exit 0 when all probes pass; non-zero on any failure.",
			Ref:       "cli/cmd/status.go",
			Output:    "stdout table",
		},
	},
}

func init() {
	registerExplain(statusCmd, &statusExplanation)
}
