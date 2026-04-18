package cmd

var inspectExplanation = explanation{
	Name:     "inspect",
	Synopsis: "Inspect the deployment bundle size and dependency offenders.",
	Summary: "`inspect` runs the same size-audit the build step uses as a " +
		"post-build warning, but reports it in full detail. Useful when " +
		"you hit the 250 MB Lambda unzipped limit and need to know which " +
		"node_modules packages are the culprits.",
	Phases: []phase{
		{
			Num:       1,
			Title:     "Resolve standalone dir",
			Narrative: "Uses the same standalone lookup as ship — .next/standalone or the tarball-extracted path. Errors clearly if no build artifact is found.",
			Ref:       "cli/cmd/inspect.go",
			Output:    "absolute path to the standalone tree",
		},
		{
			Num:       2,
			Title:     "Audit size",
			Narrative: "Walks the standalone tree, bins bytes by top-level directory and by node_modules package. Produces total, node_modules total, and a sorted list of top offenders.",
			Ref:       "internal/packaging/audit.go",
			Function:  "packaging.AuditStandaloneSize",
			Output:    "SizeReport{TotalMB, NodeModulesMB, TopOffenders[]}",
		},
		{
			Num:       3,
			Title:     "Render report",
			Narrative: "Prints a full offender table with per-package sizes and an actionable summary. Flags packages over 10 MB as likely reduction targets.",
			Ref:       "cli/cmd/inspect.go",
			Output:    "stdout table + warnings",
		},
	},
}

func init() {
	registerExplain(inspectCmd, &inspectExplanation)
}
