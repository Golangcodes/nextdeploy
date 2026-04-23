package serverless

import (
	"strings"
	"testing"

	"github.com/Golangcodes/nextdeploy/shared/config"
)

func TestResolveHyperdriveOrigin_PostgresFullURL(t *testing.T) {
	got, err := resolveHyperdriveOrigin(config.CFHyperdriveResource{
		Name:   "pesastream-db",
		Origin: "postgres://neon_user:s3cret@ep-cool.us-east-2.aws.neon.tech:5432/main?sslmode=require",
	})
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	want := hyperdriveOrigin{
		scheme:   "postgres",
		host:     "ep-cool.us-east-2.aws.neon.tech",
		port:     5432,
		user:     "neon_user",
		password: "s3cret",
		database: "main",
	}
	if got != want {
		t.Errorf("got %+v\nwant %+v", got, want)
	}
}

func TestResolveHyperdriveOrigin_PostgresqlScheme(t *testing.T) {
	// "postgresql://" should normalize to scheme "postgres"
	got, err := resolveHyperdriveOrigin(config.CFHyperdriveResource{
		Name:   "db",
		Origin: "postgresql://u:p@h.example/dbname",
	})
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if got.scheme != "postgres" {
		t.Errorf("scheme normalization: got %q want %q", got.scheme, "postgres")
	}
	if got.port != 5432 {
		t.Errorf("default postgres port not applied: got %d", got.port)
	}
}

func TestResolveHyperdriveOrigin_MySQLDefaultPort(t *testing.T) {
	got, err := resolveHyperdriveOrigin(config.CFHyperdriveResource{
		Name:   "db",
		Origin: "mysql://root:pw@db.internal/app",
	})
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if got.port != 3306 {
		t.Errorf("default mysql port not applied: got %d", got.port)
	}
}

func TestResolveHyperdriveOrigin_OriginEnvFallback(t *testing.T) {
	t.Setenv("NEON_TEST_URL", "postgres://u:p@h/d")
	got, err := resolveHyperdriveOrigin(config.CFHyperdriveResource{
		Name:      "db",
		OriginEnv: "NEON_TEST_URL",
	})
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if got.host != "h" || got.password != "p" {
		t.Errorf("env fallback failed: got %+v", got)
	}
}

func TestResolveHyperdriveOrigin_OriginLiteralBeatsEnv(t *testing.T) {
	t.Setenv("NEON_TEST_URL", "postgres://env_user:env_pw@env_host/env_db")
	got, err := resolveHyperdriveOrigin(config.CFHyperdriveResource{
		Name:      "db",
		Origin:    "postgres://lit_user:lit_pw@lit_host/lit_db",
		OriginEnv: "NEON_TEST_URL",
	})
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if got.user != "lit_user" || got.host != "lit_host" {
		t.Errorf("literal origin should win: got %+v", got)
	}
}

func TestResolveHyperdriveOrigin_Errors(t *testing.T) {
	cases := []struct {
		name     string
		decl     config.CFHyperdriveResource
		envName  string
		envValue string
		wantSub  string
	}{
		{
			name:    "missing both",
			decl:    config.CFHyperdriveResource{Name: "db"},
			wantSub: "origin or origin_env is required",
		},
		{
			name:     "empty env",
			decl:     config.CFHyperdriveResource{Name: "db", OriginEnv: "EMPTY_X"},
			envName:  "EMPTY_X",
			envValue: "",
			wantSub:  "env var EMPTY_X is empty",
		},
		{
			name:    "no scheme",
			decl:    config.CFHyperdriveResource{Name: "db", Origin: "user:p@host/db"},
			wantSub: "unsupported scheme",
		},
		{
			name:    "missing user",
			decl:    config.CFHyperdriveResource{Name: "db", Origin: "postgres://host/db"},
			wantSub: "must include user:password",
		},
		{
			name:    "missing password",
			decl:    config.CFHyperdriveResource{Name: "db", Origin: "postgres://u@host/db"},
			wantSub: "must include user:password",
		},
		{
			name:    "missing database",
			decl:    config.CFHyperdriveResource{Name: "db", Origin: "postgres://u:p@host"},
			wantSub: "must include database name",
		},
		{
			name:    "bad port",
			decl:    config.CFHyperdriveResource{Name: "db", Origin: "postgres://u:p@host:abc/db"},
			wantSub: "parse origin",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if tc.envName != "" {
				t.Setenv(tc.envName, tc.envValue)
			}
			_, err := resolveHyperdriveOrigin(tc.decl)
			if err == nil {
				t.Fatalf("want error containing %q, got nil", tc.wantSub)
			}
			if !strings.Contains(err.Error(), tc.wantSub) {
				t.Errorf("want error containing %q, got %q", tc.wantSub, err.Error())
			}
		})
	}
}
