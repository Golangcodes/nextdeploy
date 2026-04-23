package cmd

var upgradeDaemonExplanation = explanation{
	Name:     "upgrade-daemon",
	Synopsis: "Upgrade the remote NextDeploy daemon on configured VPS servers.",
	Summary: "`upgrade-daemon` (alias `update-daemon`) pushes the latest " +
		"nextdeployd binary to every configured VPS and restarts the " +
		"systemd service. Non-destructive — in-flight requests complete " +
		"before the socket closes. Only relevant for VPS targets; " +
		"serverless deploys don't run a persistent daemon.",
	Phases: []phase{
		{
			Num:       1,
			Title:     "Load config + server list",
			Narrative: "Reads nextdeploy.yml for the servers[] array. Errors if the target isn't VPS or no servers are defined.",
			Ref:       "cli/cmd/upgrade_daemon.go",
			Function:  "config.Load",
		},
		{
			Num:       2,
			Title:     "Resolve target daemon version",
			Narrative: "Either latest (default) or --version <semver>. Downloads the matching nextdeployd release tarball locally, verifies checksum.",
			Ref:       "cli/cmd/upgrade_daemon.go",
			Output:    "local tempfile with new daemon binary",
		},
		{
			Num:       3,
			Title:     "SCP + atomic replace per server",
			Narrative: "For each server: scp the new binary to /tmp, atomically replace /usr/local/bin/nextdeployd, systemctl restart nextdeployd, wait for healthcheck. Aborts remaining servers on first failure so you don't cascade a broken binary.",
			Ref:       "cli/cmd/upgrade_daemon.go",
			Function:  "server.Upload → sudo mv → systemctl restart",
		},
		{
			Num:       4,
			Title:     "Post-upgrade verify",
			Narrative: "Queries each daemon's /version endpoint to confirm the new binary is live. Prints a summary — upgraded OK, skipped (already current), failed (with reason).",
			Ref:       "cli/cmd/upgrade_daemon.go",
			Output:    "stdout summary per server",
		},
	},
}

func init() {
	registerExplain(upgradeDaemonCmd, &upgradeDaemonExplanation)
}
