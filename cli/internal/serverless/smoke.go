package serverless

import (
	"context"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/Golangcodes/nextdeploy/shared"
	"github.com/Golangcodes/nextdeploy/shared/config"
	"github.com/Golangcodes/nextdeploy/shared/nextcore"
)

// SmokeOpts tunes the post-deploy smoke check. Defaults are correct for
// typical deploys; tighten via the caller when fronting a CI workflow.
type SmokeOpts struct {
	// MaxAttempts per URL. DNS + CF cache propagation can take a few
	// seconds; 3 attempts spaced by Delay covers cold paths.
	MaxAttempts int
	// Delay between attempts for a single URL.
	Delay time.Duration
	// Timeout per individual HTTP request.
	Timeout time.Duration
	// FailOnError makes the smoke check fail the deploy on any non-2xx.
	// Default false (warn-only) — deploys sometimes race with CF caching.
	FailOnError bool
}

func defaultSmokeOpts() SmokeOpts {
	return SmokeOpts{
		MaxAttempts: 3,
		Delay:       5 * time.Second,
		Timeout:     10 * time.Second,
		FailOnError: false,
	}
}

// SmokeResult is the summary a caller logs or surfaces in CI output.
type SmokeResult struct {
	Probed []ProbeOutcome
	Passed int
	Failed int
}

type ProbeOutcome struct {
	URL        string
	StatusCode int
	Duration   time.Duration
	Attempts   int
	Err        string
}

// SmokeVerify probes a set of URLs derived from the app config + route
// manifest after a deploy completes. It's load-bearing for "did the
// deploy actually work" — the rest of the pipeline only proves the
// upload succeeded, not that the Worker responds.
//
// Non-fatal by default. Operators opting into FailOnError make the
// deploy gate on this check.
func SmokeVerify(
	ctx context.Context,
	log *shared.Logger,
	cfg *config.NextDeployConfig,
	meta *nextcore.NextCorePayload,
	opts SmokeOpts,
) (*SmokeResult, error) {
	if opts.MaxAttempts == 0 {
		opts = defaultSmokeOpts()
	}

	urls := smokeTargets(cfg, meta)
	if len(urls) == 0 {
		log.Info("Smoke verify skipped — no domain configured and no routes to probe.")
		return &SmokeResult{}, nil
	}

	client := &http.Client{Timeout: opts.Timeout}
	result := &SmokeResult{}

	log.Info("Smoke verify: probing %d URL(s)...", len(urls))
	for _, u := range urls {
		outcome := probeOnce(ctx, client, u, opts)
		result.Probed = append(result.Probed, outcome)
		if outcome.StatusCode >= 200 && outcome.StatusCode < 400 {
			result.Passed++
			log.Info("  ✓ %s → %d (%s, %d attempt(s))", u, outcome.StatusCode, outcome.Duration.Round(time.Millisecond), outcome.Attempts)
			continue
		}
		result.Failed++
		if outcome.Err != "" {
			log.Warn("  ✗ %s failed after %d attempt(s): %s", u, outcome.Attempts, outcome.Err)
		} else {
			log.Warn("  ✗ %s → %d (non-2xx/3xx after %d attempt(s))", u, outcome.StatusCode, outcome.Attempts)
		}
	}

	log.Info("Smoke verify: %d passed, %d failed", result.Passed, result.Failed)
	if opts.FailOnError && result.Failed > 0 {
		return result, fmt.Errorf("smoke verify failed: %d of %d probes did not return 2xx/3xx", result.Failed, len(urls))
	}
	return result, nil
}

// smokeTargets builds the probe list. Prefers cfg.App.Domain (root)
// plus up to 3 declared static routes. Kept small so we don't hammer
// the deployment or wait minutes for the check to complete.
func smokeTargets(cfg *config.NextDeployConfig, meta *nextcore.NextCorePayload) []string {
	domain := strings.TrimSpace(cfg.App.Domain)
	if domain == "" {
		return nil
	}
	scheme := "https://"
	if strings.HasPrefix(domain, "http://") || strings.HasPrefix(domain, "https://") {
		scheme = ""
	}
	base := strings.TrimSuffix(scheme+domain, "/")

	urls := []string{base + "/"}
	if meta != nil {
		for i, path := range meta.RouteInfo.StaticRoutes {
			if i >= 3 {
				break
			}
			if path == "" || path == "/" {
				continue
			}
			urls = append(urls, base+path)
		}
	}
	return urls
}

// probeOnce does up to MaxAttempts GETs against url. Returns on first
// 2xx/3xx; exhausts attempts on persistent failure.
func probeOnce(ctx context.Context, client *http.Client, url string, opts SmokeOpts) ProbeOutcome {
	out := ProbeOutcome{URL: url}
	start := time.Now()
	for attempt := 1; attempt <= opts.MaxAttempts; attempt++ {
		out.Attempts = attempt
		attemptStart := time.Now()
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
		if err != nil {
			out.Err = err.Error()
			break
		}
		req.Header.Set("User-Agent", "nextdeploy-smoke/1.0")
		resp, err := client.Do(req)
		out.Duration = time.Since(attemptStart)
		if err != nil {
			out.Err = err.Error()
			if attempt < opts.MaxAttempts {
				select {
				case <-ctx.Done():
					return out
				case <-time.After(opts.Delay):
				}
				continue
			}
			break
		}
		_ = resp.Body.Close()
		out.StatusCode = resp.StatusCode
		out.Err = ""
		if resp.StatusCode < 400 {
			break
		}
		if attempt < opts.MaxAttempts {
			select {
			case <-ctx.Done():
				return out
			case <-time.After(opts.Delay):
			}
		}
	}
	out.Duration = time.Since(start)
	return out
}
