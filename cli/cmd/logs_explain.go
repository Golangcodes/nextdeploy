package cmd

var logsExplanation = explanation{
	Name:     "logs",
	Synopsis: "Stream application logs from the running deployment.",
	Summary: "`logs` tails whatever logging surface the target provider " +
		"exposes. VPS: connects to the nextdeploy daemon's log aggregator " +
		"which colorizes and de-noises systemd + container output. " +
		"Serverless AWS: streams CloudWatch for the Lambda function. " +
		"Serverless Cloudflare: uses `wrangler tail` under the hood when " +
		"available.",
	Phases: []phase{
		{
			Num:       1,
			Title:     "Load config + resolve target",
			Narrative: "Same config load as ship. Target determines which log backend to connect to.",
			Ref:       "cli/cmd/logs.go",
			Function:  "config.Load",
		},
		{
			Num:       2,
			Title:     "Open log stream",
			Narrative: "VPS: opens an SSH session and subscribes to the daemon's /logs/stream. AWS: starts a CloudWatch Logs tail with the Lambda log group. Cloudflare: spawns wrangler tail.",
			Ref:       "cli/cmd/logs.go",
			Output:    "io.Reader of log events",
		},
		{
			Num:       3,
			Title:     "Aggregate + colorize",
			Narrative: "Log events are routed through shared/nextdeploy/log_aggregator, which strips journalctl metadata, dedupes noise, and colorizes by severity. Output writes to stdout with --follow behavior.",
			Ref:       "shared/nextdeploy/log_aggregator.go",
			Function:  "LogAggregator.Stream",
		},
		{
			Num:       4,
			Title:     "Terminate",
			Narrative: "Ctrl-C cleanly closes the remote stream and unwinds SSH/CloudWatch/wrangler sessions.",
			Ref:       "cli/cmd/logs.go",
		},
	},
}

func init() {
	registerExplain(logsCmd, &logsExplanation)
}
