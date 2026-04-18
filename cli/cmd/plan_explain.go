package cmd

var planExplanation = explanation{
	Name:     "plan",
	Synopsis: "Dry-run: show what nextdeploy would create, update, or flag as drift.",
	Summary: "`plan` is the Terraform-style diff for Cloudflare deployments. " +
		"Read-only: inspects every resource declared under " +
		"serverless.cloudflare.resources and emits a PlanResult with " +
		"per-resource actions (create / update / no-op / drift). Exits " +
		"non-zero (code 2) when immutable-drift is detected so CI can " +
		"gate on it.",
	Phases: []phase{
		{
			Num:       1,
			Title:     "Validate config target",
			Narrative: "Plan is Cloudflare-only today. Missing cloudflare block → fatal. AWS deploys use the cloud-native plan tooling (terraform/cdk) instead.",
			Ref:       "cli/cmd/plan.go:44",
			Output:    "fatal if provider != cloudflare",
		},
		{
			Num:       2,
			Title:     "Initialize provider",
			Narrative: "Same CF SDK wiring as ship's step 5 — loads creds, verifies token, creates the R2-S3 client. Read-only operations need the same auth as writes.",
			Ref:       "cli/internal/serverless/cloudflare.go:153",
			Function:  "CloudflareProvider.Initialize",
		},
		{
			Num:       3,
			Title:     "Plan resources",
			Narrative: "Walks every resource kind (Hyperdrive, Queues, Vectorize, AI Gateway, DNS) and compares declared config vs. current CF state. Produces []PlanItem entries with per-resource verdicts.",
			Ref:       "cli/internal/serverless/cloudflare_plan.go:58",
			Function:  "CloudflareProvider.Plan",
			Input:     "*config.NextDeployConfig",
			Output:    "*PlanResult with PlanCreate / PlanUpdate / PlanNoOp / PlanImmutableDrift",
		},
		{
			Num:       4,
			Title:     "--only filter (optional)",
			Narrative: "Narrows output to specific resource kinds. Useful in CI pipelines that only want to block on specific drift (e.g. `plan --only dns,queues`).",
			Ref:       "cli/cmd/plan.go",
			Function:  "filterPlanByModules",
		},
		{
			Num:       5,
			Title:     "Render plan",
			Narrative: "Prints the results in a grouped-by-kind table, colored by action. Immutable-drift rows are loudest — those require manual intervention.",
			Ref:       "cli/cmd/plan.go",
			Function:  "renderPlan",
			Output:    "stdout table",
		},
		{
			Num:       6,
			Title:     "Exit code",
			Narrative: "Exit 0 on clean + create/update/no-op. Exit 2 on any immutable-drift so CI can fail the build. Non-CI callers inspect the printed output.",
			Ref:       "cli/cmd/plan.go",
			Output:    "exit 0 / 2",
		},
	},
}

func init() {
	registerExplain(planCmd, &planExplanation)
}
