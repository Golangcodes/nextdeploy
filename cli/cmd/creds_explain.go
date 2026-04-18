package cmd

var credsExplanation = explanation{
	Name:     "creds",
	Synopsis: "Manage cloud-provider credentials in the encrypted credstore.",
	Summary: "`creds` manages the encrypted credential store at " +
		"~/.nextdeploy/credstore (mode 0600). AWS + Cloudflare credentials " +
		"resolve in order: env vars first, then this credstore, then " +
		"(legacy) plaintext in nextdeploy.yml. Using the credstore keeps " +
		"secrets off disk in cleartext and out of CI logs.",
	Phases: []phase{
		{
			Num:       1,
			Title:     "creds set --provider <aws|cloudflare>",
			Narrative: "Interactive prompts for each field the provider needs: AWS wants access_key + secret_key; Cloudflare wants api_token + account_id + R2 access/secret. Values are encrypted with the per-machine key and written to ~/.nextdeploy/credstore.",
			Ref:       "cli/cmd/creds.go:55",
			Function:  "credstore.Save",
			Output:    "encrypted credstore entry",
		},
		{
			Num:       2,
			Title:     "creds clear --provider <aws|cloudflare>",
			Narrative: "Removes one provider's entry from the credstore. The ship path will fall back to env vars or yaml on the next deploy.",
			Ref:       "cli/cmd/creds.go:101",
			Function:  "credstore.Delete",
		},
		{
			Num:       3,
			Title:     "creds list",
			Narrative: "Shows which providers have credstore entries, without revealing values. Useful for verifying the credstore is populated before a deploy.",
			Ref:       "cli/cmd/creds.go",
			Function:  "credstore.List",
		},
	},
}

func init() {
	registerExplain(credsCmd, &credsExplanation)
}
