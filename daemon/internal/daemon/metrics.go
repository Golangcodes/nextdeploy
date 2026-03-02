package daemon

import (
	"expvar"
	"fmt"
	"net/http"

	// #nosec G108
	_ "net/http/pprof"
	"runtime"
	"time"

	"github.com/Golangcodes/nextdeploy/shared"
)

var (
	CommandsHandled = expvar.NewInt("commands_handled")
	RequestsTotal   = expvar.NewInt("requests_total")
	StartTime       = time.Now()
)

func init() {
	expvar.NewString("version").Set(shared.Version)
	expvar.NewString("started_at").Set(StartTime.UTC().Format(time.RFC3339))
	expvar.Publish("goroutines", expvar.Func(func() any {
		return runtime.NumGoroutine()
	}))
	expvar.Publish("uptime_seconds", expvar.Func(func() any {
		return int64(time.Since(StartTime).Seconds())
	}))
}

func StartMetricsServer(addr string) {
	go func() {
		mux := http.NewServeMux()
		mux.Handle("/debug/vars", expvar.Handler())
		mux.Handle("/debug/pprof/", http.DefaultServeMux)
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
