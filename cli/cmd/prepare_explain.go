package cmd

var prepareExplanation = explanation{
	Name:     "prepare",
	Synopsis: "Provision a VPS with the tools NextDeploy's daemon needs.",
	Summary: "`prepare` runs a self-contained Ansible playbook against the " +
		"declared VPS server(s) to install Docker, Caddy, the NextDeploy " +
		"daemon, and the minimal supporting toolchain. Idempotent — " +
		"re-running on a prepared server is a fast no-op. Intended as a " +
		"one-shot onboarding step before the first `ship`.",
	Phases: []phase{
		{
			Num:       1,
			Title:     "Context + signal handling",
			Narrative: "Sets a timeout for the whole run (default long — Ansible over slow SSH can take minutes) and installs a SIGINT/SIGTERM trap so Ctrl-C cancels cleanly.",
			Ref:       "cli/cmd/prepare.go:60",
			Output:    "context.Context with cancellation",
		},
		{
			Num:       2,
			Title:     "Ensure Ansible installed",
			Narrative: "Checks for ansible-playbook in PATH. If missing, iterates through platform-aware installers (pipx / apt / dnf / yum / brew / pip) and uses the first whose prereq binary is available. Fatal if none works.",
			Ref:       "cli/cmd/prepare.go:73",
			Function:  "ensureAnsible",
			Notes:     []string{"Prefers pipx on all platforms — cleanest isolation from system Python."},
		},
		{
			Num:       3,
			Title:     "Resolve target server",
			Narrative: "Reads nextdeploy.yml's servers[] list. Uses --server flag to pick when multiple are defined; errors if ambiguous and --server wasn't set.",
			Ref:       "cli/cmd/prepare.go:78",
			Function:  "resolveTargetServer",
			Output:    "serverName, ServerConfig",
		},
		{
			Num:       4,
			Title:     "Write Ansible inputs",
			Narrative: "Creates a temp dir and writes two files: a minimal inventory pointing at the target host (SSH user + key) and the embedded prepare playbook verbatim. Temp dir is cleaned up on exit.",
			Ref:       "cli/cmd/prepare.go:87",
			Function:  "writeInventory + os.WriteFile(playbook)",
			Output:    "tmpDir/inventory + tmpDir/prepare.yml",
		},
		{
			Num:       5,
			Title:     "Run ansible-playbook",
			Narrative: "Spawns ansible-playbook with the temp inventory + playbook. Streams output live so operators see progress. --verbose propagates to -vvv.",
			Ref:       "cli/cmd/prepare.go:106",
			Function:  "runAnsible",
			Notes:     []string{"Playbook installs Docker, Caddy, the nextdeployd binary, systemd service, and the install-verify smoke check."},
		},
	},
}

func init() {
	registerExplain(prepareCmd, &prepareExplanation)
}
