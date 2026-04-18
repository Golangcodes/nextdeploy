package cmd

var destroyExplanation = explanation{
	Name:     "destroy",
	Synopsis: "Remove all deployed resources for this app.",
	Summary: "`destroy` tears down the cloud-side footprint of the app: " +
		"serverless compute + CDN/storage for AWS or Cloudflare, or the " +
		"on-disk installation on VPS targets. Driven by the same " +
		"nextdeploy.yml as ship — resource names are derived from app " +
		"metadata, so stale configs will miss resources.",
	Phases: []phase{
		{
			Num:       1,
			Title:     "Load config + metadata",
			Narrative: "Reads nextdeploy.yml for target type + provider, and .nextdeploy/metadata.json for the app name. Missing metadata is a warning — destroy proceeds with whatever cfg declares.",
			Ref:       "cli/cmd/destroy.go:22",
			Function:  "config.Load + json.Unmarshal(metadata.json)",
			Output:    "*NextDeployConfig + NextCorePayload",
		},
		{
			Num:       2,
			Title:     "Branch by target type",
			Narrative: "Serverless route initializes the provider (AWS/Cloudflare) and calls Destroy. VPS route opens an SSH session to each configured server and issues a daemon-side cleanup command.",
			Ref:       "cli/cmd/destroy.go:41",
			Output:    "dispatched to serverless or vps destroyer",
		},
		{
			Num:       3,
			Title:     "Provider Destroy",
			Narrative: "Cloudflare: deletes the Worker script + R2 bucket. AWS: removes Lambda functions, CloudFront distribution, S3 bucket, Secrets Manager entries (best-effort). 10-minute context deadline.",
			Ref:       "cli/internal/serverless/cloudflare.go:618",
			Function:  "Provider.Destroy",
			Notes:     []string{"R2 bucket delete fails if non-empty; we don't sweep objects yet."},
		},
		{
			Num:       4,
			Title:     "DNS guidance",
			Narrative: "Prints the list of DNS records the user should remove from their DNS provider (custom domain CNAMEs, verification TXTs). Manual step — Cloudflare-managed zones get cleaned automatically.",
			Ref:       "cli/cmd/destroy.go",
			Output:    "printed instructions",
		},
	},
}

func init() {
	registerExplain(destroyCmd, &destroyExplanation)
}
