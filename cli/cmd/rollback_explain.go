package cmd

var rollbackExplanation = explanation{
	Name:     "rollback",
	Synopsis: "Revert to a previous deployment — instantly, no rebuild.",
	Summary: "`rollback` re-points the live workload at an older version of " +
		"the compute layer. Serverless routes through the provider's own " +
		"versioning (Lambda aliases / Worker deployments); VPS uses the " +
		"daemon's `releases/` directory and just flips the `current` " +
		"symlink. Two mutually exclusive modes: --steps N walks back N " +
		"deployments, --to <commit> pins to a specific git commit (when " +
		"the provider supports commit-tagged history).",
	Phases: []phase{
		{
			Num:       1,
			Title:     "Validate flags",
			Narrative: "--steps must be non-negative; --to and --steps > 1 are mutually exclusive. Early exit with clear error avoids the provider learning about the bad input.",
			Ref:       "cli/cmd/rollback.go:34",
			Output:    "fatal on invalid combination",
		},
		{
			Num:       2,
			Title:     "Load config + resolve target",
			Narrative: "Same loader as ship. TargetType drives which rollback path runs — serverless via the provider, vps via SSH + daemon.",
			Ref:       "cli/cmd/rollback.go:44",
			Function:  "config.Load → cfg.TargetType",
		},
		{
			Num:       3,
			Title:     "Serverless rollback",
			Narrative: "AWS: selects the prior Lambda alias target. Cloudflare: activates a previous Worker deployment version via Workers.Scripts.Deployments. CF doesn't track git commits so --to degrades to --steps with a warning.",
			Ref:       "cli/internal/serverless/cloudflare.go:566",
			Function:  "serverless.Rollback → Provider.Rollback",
			Input:     "RollbackOptions{Steps, ToCommit}",
			Output:    "prior version active",
			Notes:     []string{"CF: Workers.Scripts.Deployments.New with 100% traffic on previous version."},
		},
		{
			Num:       4,
			Title:     "VPS rollback (alternative path)",
			Narrative: "Opens an SSH session to each configured server and issues a daemon rollback command. Daemon flips the `current` symlink in /opt/nextdeploy/apps/<app>/releases/ to the prior release and restarts the systemd service.",
			Ref:       "cli/cmd/rollback.go:66",
			Function:  "server.New → ssh into daemon",
		},
	},
}

func init() {
	registerExplain(rollbackCmd, &rollbackExplanation)
}
