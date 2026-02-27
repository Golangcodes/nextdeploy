// Package daemon — metrics.go
// Exposes internal daemon state via the standard expvar package on
// localhost:6060/debug/vars. The endpoint is intentionally localhost-only
// and never bound to a public interface.
package daemon

import (
	"expvar"
	"fmt"
	"net/http"
	_ "net/http/pprof" // registers /debug/pprof alongside /debug/vars
	"runtime"
	"time"

	"github.com/Golangcodes/nextdeploy/shared"
)

// Daemon-wide counters — increment these from other daemon code as needed.
var (
	// CommandsHandled is the total number of socket commands processed.
	CommandsHandled = expvar.NewInt("commands_handled")

	// RequestsTotal is a general-purpose request counter.
	RequestsTotal = expvar.NewInt("requests_total")

	// StartTime records when the daemon process started.
	StartTime = time.Now()
)

func init() {
	// Publish static build metadata so operators can verify what's running.
	expvar.NewString("version").Set(shared.Version)
	expvar.NewString("started_at").Set(StartTime.UTC().Format(time.RFC3339))

	// Publish a live goroutine counter so the mage HealthCheck can read it.
	expvar.Publish("goroutines", expvar.Func(func() any {
		return runtime.NumGoroutine()
	}))

	// Uptime in seconds.
	expvar.Publish("uptime_seconds", expvar.Func(func() any {
		return int64(time.Since(StartTime).Seconds())
	}))
}

// StartMetricsServer starts the expvar + pprof HTTP server on localhost only.
// It is non-blocking — runs in a goroutine and logs errors to stderr.
// addr should be something like "127.0.0.1:6060".
func StartMetricsServer(addr string) {
	go func() {
		mux := http.NewServeMux()

		// expvar handler (publishes /debug/vars JSON)
		mux.Handle("/debug/vars", expvar.Handler())

		// pprof routes (already registered on DefaultServeMux by the import above)
		mux.Handle("/debug/pprof/", http.DefaultServeMux)

		// Friendly root redirect
		mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
			http.Redirect(w, r, "/debug/vars", http.StatusFound)
		})

		srv := &http.Server{
			Addr:         addr,
			Handler:      mux,
			ReadTimeout:  5 * time.Second,
			WriteTimeout: 10 * time.Second,
		}

		fmt.Printf("[nextdeployd] metrics available at http://%s/debug/vars\n", addr)
		if err := srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			fmt.Printf("[nextdeployd] metrics server error: %v\n", err)
		}
	}()
}
