package cmd

const secretsGoFile = "cli/cmd/secrets.go"

var secretsExplanation = explanation{
	Name:     "secrets",
	Synopsis: "Manage the managed JSON secret store (.nextdeploy/.env).",
	Summary: "`secrets` is the CLI surface over NextDeploy's managed secret " +
		"file (.nextdeploy/.env). It's one of three secret sources that " +
		"ship merges at deploy time — the other two are the project-root " +
		".env and any files declared under secrets.files[] in " +
		"nextdeploy.yml. Secrets set here have the highest precedence " +
		"(explicit user intent wins). Subcommands: set, get, unset, list, " +
		"load, sync.",
	Phases: []phase{
		{
			Num:       1,
			Title:     "secrets set KEY=VALUE…",
			Narrative: "Parses KEY=VALUE args (multiple allowed) and writes them into the managed store via SecretManager.Set. File is created with 0600 perms on first write. Values appear in the next `ship` without extra steps.",
			Ref:       secretsGoFile + ":28",
			Function:  "secrets.NewSecretManager → .Set",
			Output:    ".nextdeploy/.env updated",
		},
		{
			Num:       2,
			Title:     "secrets get KEY",
			Narrative: "Reads one value from the managed store. Redacted by default (prints '[set]' or last-4 chars) — pass --reveal for the full value. Does not touch cloud-side secrets.",
			Ref:       secretsGoFile + ":42",
			Function:  "secrets.NewSecretManager → .Get",
		},
		{
			Num:       3,
			Title:     "secrets unset KEY",
			Narrative: "Removes a key from the managed store. No cloud-side action — the next ship will stop pushing this secret, but existing values in Secrets Manager / CF Worker secrets persist until a sync/ship runs.",
			Ref:       secretsGoFile,
			Function:  "secrets.NewSecretManager → .Unset",
		},
		{
			Num:       4,
			Title:     "secrets list",
			Narrative: "Prints all keys (values redacted). Useful for auditing what will ship.",
			Ref:       secretsGoFile,
			Function:  "secrets.NewSecretManager → .FlattenSecrets",
		},
		{
			Num:       5,
			Title:     "secrets load FILE",
			Narrative: "Imports a dotenv file into the managed store. Existing keys are overwritten. Same parser as ship's .env ingestion.",
			Ref:       secretsGoFile,
			Function:  "secrets.NewSecretManager → .ImportSecrets",
		},
		{
			Num:       6,
			Title:     "secrets sync",
			Narrative: "Pushes the current managed store state to the cloud provider without a full ship. Useful when rotating a key mid-cycle. Calls Provider.UpdateSecrets identically to ship's secret-push step.",
			Ref:       secretsGoFile,
			Function:  "Provider.UpdateSecrets",
		},
	},
}

func init() {
	registerExplain(secretsCmd, &secretsExplanation)
}
