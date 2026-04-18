package serverless

import (
	"context"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Golangcodes/nextdeploy/shared"
	"github.com/Golangcodes/nextdeploy/shared/config"
	"github.com/Golangcodes/nextdeploy/shared/nextcore"
)

func TestSmokeVerify_NoDomainSkips(t *testing.T) {
	log := shared.PackageLogger("test", "")
	cfg := &config.NextDeployConfig{}
	res, err := SmokeVerify(context.Background(), log, cfg, nil, SmokeOpts{MaxAttempts: 1})
	if err != nil {
		t.Fatalf("SmokeVerify: %v", err)
	}
	if len(res.Probed) != 0 {
		t.Errorf("expected no probes without domain, got %+v", res.Probed)
	}
}

func TestSmokeVerify_PassingEndpoint(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	t.Cleanup(server.Close)

	cfg := &config.NextDeployConfig{}
	// Keep the http:// scheme so smokeTargets doesn't force https:// onto
	// a plain-HTTP httptest server.
	cfg.App.Domain = server.URL

	log := shared.PackageLogger("test", "")
	res, err := SmokeVerify(context.Background(), log, cfg,
		&nextcore.NextCorePayload{}, // no static routes — just root probe
		SmokeOpts{MaxAttempts: 1, Timeout: 2 * time.Second, Delay: 10 * time.Millisecond})
	if err != nil {
		t.Fatalf("SmokeVerify: %v", err)
	}
	if res.Passed != 1 || res.Failed != 0 {
		t.Errorf("got passed=%d failed=%d: %+v", res.Passed, res.Failed, res.Probed)
	}
}

func TestSmokeVerify_RetriesOn5xxThenSucceeds(t *testing.T) {
	var hits atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		n := hits.Add(1)
		if n < 3 {
			w.WriteHeader(http.StatusBadGateway)
			return
		}
		w.WriteHeader(http.StatusOK)
	}))
	t.Cleanup(server.Close)

	cfg := &config.NextDeployConfig{}
	cfg.App.Domain = server.URL

	log := shared.PackageLogger("test", "")
	res, err := SmokeVerify(context.Background(), log, cfg, nil, SmokeOpts{
		MaxAttempts: 3, Timeout: 2 * time.Second, Delay: 10 * time.Millisecond,
	})
	if err != nil {
		t.Fatalf("SmokeVerify: %v", err)
	}
	if res.Passed != 1 {
		t.Errorf("expected passing after retries, got %+v", res.Probed)
	}
	if res.Probed[0].Attempts != 3 {
		t.Errorf("expected 3 attempts, got %d", res.Probed[0].Attempts)
	}
}

func TestSmokeVerify_FailOnErrorGates(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	t.Cleanup(server.Close)

	cfg := &config.NextDeployConfig{}
	cfg.App.Domain = server.URL

	log := shared.PackageLogger("test", "")
	_, err := SmokeVerify(context.Background(), log, cfg, nil, SmokeOpts{
		MaxAttempts: 1, Timeout: 2 * time.Second, Delay: 10 * time.Millisecond,
		FailOnError: true,
	})
	if err == nil {
		t.Fatal("expected FailOnError to gate on persistent 5xx")
	}
}

func TestSmokeTargets_PrefersRootPlusFewStatics(t *testing.T) {
	cfg := &config.NextDeployConfig{}
	cfg.App.Domain = "example.com"
	meta := &nextcore.NextCorePayload{
		RouteInfo: nextcore.RouteInfo{
			StaticRoutes: []string{"/", "/about", "/contact", "/docs", "/pricing"},
		},
	}
	urls := smokeTargets(cfg, meta)
	// Root + at most 3 statics (skipping "/")
	if len(urls) < 2 || len(urls) > 4 {
		t.Errorf("expected 2-4 urls, got %v", urls)
	}
	if urls[0] != "https://example.com/" {
		t.Errorf("first URL should be root, got %s", urls[0])
	}
}
