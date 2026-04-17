package cmd

import (
	"context"
	"fmt"
	"os"
	"strings"

	"github.com/Golangcodes/nextdeploy/cli/internal/serverless"
	"github.com/Golangcodes/nextdeploy/shared"
	"github.com/Golangcodes/nextdeploy/shared/config"
	"github.com/spf13/cobra"
)

// planOnlyFlag is a comma-separated list of module names to scope the plan to.
// Recognized values: dataplane (hyperdrive, queues, vectorize, ai_gateway),
// edge (dns). Empty means "all modules". Unknown values error out — typos
// silently scoping to nothing would be a footgun on a production cutover.
var planOnlyFlag string

var planCmd = &cobra.Command{
	Use:   "plan",
	Short: "Dry-run: show what nextdeploy would create or change",
	Long: `Reads cloudflare.resources.* from nextdeploy.yml, queries the Cloudflare
API for current state, and prints the diff without making any changes.

Exit codes:
  0  plan computed cleanly
  1  unable to compute plan (auth, network, etc.)
  2  immutable drift detected (e.g. Vectorize dimension mismatch)

Currently supports the IaC layer (hyperdrive, queues, vectorize, ai_gateway,
dns). Worker bindings, custom domains, and routes are evaluated at deploy
time and are not part of the plan.`,
	Run: func(cmd *cobra.Command, args []string) {
		log := shared.PackageLogger("plan", "📋 PLAN")

		cfg, err := config.Load()
		if err != nil {
			log.Error("Failed to load config: %v", err)
			os.Exit(1)
		}

		if cfg.Serverless == nil || cfg.Serverless.Cloudflare == nil {
			log.Error("plan only supports Cloudflare deployments — no serverless.cloudflare block found")
			os.Exit(1)
		}

		ctx := context.Background()
		provider := serverless.NewCloudflareProvider()
		if err := provider.Initialize(ctx, cfg); err != nil {
			log.Error("Cloudflare initialize failed: %v", err)
			os.Exit(1)
		}

		result, err := provider.Plan(ctx, cfg)
		if err != nil {
			log.Error("Plan failed: %v", err)
			os.Exit(1)
		}

		if planOnlyFlag != "" {
			filtered, err := filterPlanByModules(result, planOnlyFlag)
			if err != nil {
				log.Error("--only: %v", err)
				os.Exit(1)
			}
			result = filtered
		}

		renderPlan(result)

		if result.HasDrift() {
			os.Exit(2)
		}
	},
}

func renderPlan(r *serverless.PlanResult) {
	if len(r.Items) == 0 {
		fmt.Println("No declared resources found under cloudflare.resources.*")
		return
	}

	creates, updates, noops, drifts := 0, 0, 0, 0
	for _, it := range r.Items {
		switch it.Action {
		case serverless.PlanCreate:
			creates++
		case serverless.PlanUpdate:
			updates++
		case serverless.PlanNoOp:
			noops++
		case serverless.PlanImmutableDrift:
			drifts++
		}
	}

	fmt.Println()
	fmt.Println("Cloudflare IaC Plan")
	fmt.Println("===================")
	for _, it := range r.Items {
		var marker string
		switch it.Action {
		case serverless.PlanCreate:
			marker = success("+")
		case serverless.PlanUpdate:
			marker = warning("~")
		case serverless.PlanNoOp:
			marker = " "
		case serverless.PlanImmutableDrift:
			marker = errorMsg("!")
		}
		line := fmt.Sprintf("  %s %-12s %s", marker, it.Kind, it.Name)
		if it.Detail != "" {
			line += "  " + it.Detail
		}
		fmt.Println(line)
	}
	fmt.Println()
	fmt.Printf("Summary: %d create, %d update, %d no-op, %d drift\n", creates, updates, noops, drifts)
	if drifts > 0 {
		fmt.Println()
		fmt.Println(errorMsg("✗ Immutable drift detected — manual intervention required."))
	}
}

// kindToModule maps a PlanItem.Kind to the module bucket --only filters by.
// Unmapped kinds are dropped from any --only output.
var kindToModule = map[string]string{
	"hyperdrive": "dataplane",
	"queue":      "dataplane",
	"vectorize":  "dataplane",
	"ai_gateway": "dataplane",
	"dns":        "edge",
}

var validModules = map[string]bool{
	"dataplane": true,
	"edge":      true,
}

func filterPlanByModules(r *serverless.PlanResult, only string) (*serverless.PlanResult, error) {
	want := map[string]bool{}
	for _, m := range strings.Split(only, ",") {
		m = strings.TrimSpace(strings.ToLower(m))
		if m == "" {
			continue
		}
		if !validModules[m] {
			return nil, fmt.Errorf("unknown module %q (valid: dataplane, edge)", m)
		}
		want[m] = true
	}
	if len(want) == 0 {
		return r, nil
	}
	out := &serverless.PlanResult{}
	for _, it := range r.Items {
		if want[kindToModule[it.Kind]] {
			out.Items = append(out.Items, it)
		}
	}
	return out, nil
}

func init() {
	planCmd.Flags().StringVar(&planOnlyFlag, "only", "", "Scope plan to module(s): comma-separated list of {dataplane,edge}")
	rootCmd.AddCommand(planCmd)
}
