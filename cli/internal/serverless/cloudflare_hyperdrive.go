package serverless

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"os"
	"strconv"
	"strings"

	"github.com/Golangcodes/nextdeploy/shared/config"
	"github.com/Golangcodes/nextdeploy/shared/sensitive"

	"github.com/cloudflare/cloudflare-go/v6"
	"github.com/cloudflare/cloudflare-go/v6/hyperdrive"
)

// ensureHyperdrive creates or updates a Hyperdrive config matching the user's
// declaration. Returns the CF UUID for binding resolution.
//
// Idempotency: list by name → match → on miss create, on match update with
// fresh password (CF API never returns the password; we always re-send it).
//
// Drift policy:
//   - Non-credential drift (host/port/db/user) is allowed via Update — CF
//     supports hot-swap on those fields. Pesastream §10's "delete+recreate"
//     concern referred to a now-fixed SDK gap; v6 has Edit/Update.
//   - Caching/connection_limit drift is also Update-handled.
func (p *CloudflareProvider) ensureHyperdrive(ctx context.Context, decl config.CFHyperdriveResource) (string, error) {
	parsed, err := resolveHyperdriveOrigin(decl)
	if err != nil {
		return "", err
	}
	sensitive.Register(parsed.password)

	originParam := hyperdrive.HyperdriveOriginPublicDatabaseParam{
		Database: cloudflare.F(parsed.database),
		Host:     cloudflare.F(parsed.host),
		Password: cloudflare.F(parsed.password),
		Port:     cloudflare.F(int64(parsed.port)),
		Scheme:   cloudflare.F(hyperdrive.HyperdriveOriginPublicDatabaseScheme(parsed.scheme)),
		User:     cloudflare.F(parsed.user),
	}

	// Find existing by name.
	existingID, err := p.findHyperdriveID(ctx, decl.Name)
	if err != nil {
		return "", fmt.Errorf("list hyperdrive configs: %w", err)
	}

	body := hyperdrive.HyperdriveParam{
		Name:   cloudflare.F(decl.Name),
		Origin: cloudflare.F[hyperdrive.HyperdriveOriginUnionParam](originParam),
	}

	if existingID != "" {
		updated, err := p.cf.Hyperdrive.Configs.Update(ctx, existingID, hyperdrive.ConfigUpdateParams{
			AccountID:  cloudflare.F(p.accountID),
			Hyperdrive: body,
		})
		if err != nil {
			return "", fmt.Errorf("update hyperdrive %q: %w", decl.Name, err)
		}
		p.log.Info("Hyperdrive config updated: %s (id=%s)", decl.Name, updated.ID)
		return updated.ID, nil
	}

	created, err := p.cf.Hyperdrive.Configs.New(ctx, hyperdrive.ConfigNewParams{
		AccountID:  cloudflare.F(p.accountID),
		Hyperdrive: body,
	})
	if err != nil {
		return "", fmt.Errorf("create hyperdrive %q: %w", decl.Name, err)
	}
	p.log.Info("Hyperdrive config created: %s (id=%s)", decl.Name, created.ID)
	return created.ID, nil
}

// findHyperdriveID returns the UUID of the Hyperdrive config with the given
// name, or "" if none exists. Returns an error only on transport / non-404
// API failures (so callers don't mistake "not found" for a real error).
func (p *CloudflareProvider) findHyperdriveID(ctx context.Context, name string) (string, error) {
	iter := p.cf.Hyperdrive.Configs.ListAutoPaging(ctx, hyperdrive.ConfigListParams{
		AccountID: cloudflare.F(p.accountID),
	})
	for iter.Next() {
		h := iter.Current()
		if h.Name == name {
			return h.ID, nil
		}
	}
	if err := iter.Err(); err != nil {
		var apiErr *cloudflare.Error
		if errors.As(err, &apiErr) && apiErr.StatusCode == http.StatusNotFound {
			return "", nil
		}
		return "", err
	}
	return "", nil
}

// hyperdriveOrigin holds the parsed components of a Postgres/MySQL connection
// string. Kept unexported — only ensureHyperdrive consumes it.
type hyperdriveOrigin struct {
	scheme   string // "postgres" | "mysql"
	host     string
	port     int
	user     string
	password string
	database string
}

// resolveHyperdriveOrigin pulls the connection string out of decl and parses
// it. decl.Origin (literal) takes precedence over decl.OriginEnv (env var).
// Errors are descriptive — connection-string mistakes are the #1 user pain.
func resolveHyperdriveOrigin(decl config.CFHyperdriveResource) (hyperdriveOrigin, error) {
	raw := strings.TrimSpace(decl.Origin)
	if raw == "" && decl.OriginEnv != "" {
		raw = strings.TrimSpace(os.Getenv(decl.OriginEnv))
		if raw == "" {
			return hyperdriveOrigin{}, fmt.Errorf("hyperdrive %q: env var %s is empty", decl.Name, decl.OriginEnv)
		}
	}
	if raw == "" {
		return hyperdriveOrigin{}, fmt.Errorf("hyperdrive %q: origin or origin_env is required", decl.Name)
	}

	u, err := url.Parse(raw)
	if err != nil {
		return hyperdriveOrigin{}, fmt.Errorf("hyperdrive %q: parse origin: %w", decl.Name, err)
	}

	scheme := strings.ToLower(u.Scheme)
	switch scheme {
	case "postgres", "postgresql":
		scheme = "postgres"
	case "mysql":
		// mysql kept as-is; CF accepts both
	default:
		return hyperdriveOrigin{}, fmt.Errorf("hyperdrive %q: unsupported scheme %q (want postgres/postgresql/mysql)", decl.Name, u.Scheme)
	}

	host := u.Hostname()
	if host == "" {
		return hyperdriveOrigin{}, fmt.Errorf("hyperdrive %q: origin missing host", decl.Name)
	}
	port := 5432
	if scheme == "mysql" {
		port = 3306
	}
	if ps := u.Port(); ps != "" {
		n, err := strconv.Atoi(ps)
		if err != nil {
			return hyperdriveOrigin{}, fmt.Errorf("hyperdrive %q: bad port %q", decl.Name, ps)
		}
		port = n
	}

	user := u.User.Username()
	password, _ := u.User.Password()
	if user == "" || password == "" {
		return hyperdriveOrigin{}, fmt.Errorf("hyperdrive %q: origin must include user:password (got user=%q)", decl.Name, user)
	}

	database := strings.TrimPrefix(u.Path, "/")
	if database == "" {
		return hyperdriveOrigin{}, fmt.Errorf("hyperdrive %q: origin must include database name in path", decl.Name)
	}

	return hyperdriveOrigin{
		scheme:   scheme,
		host:     host,
		port:     port,
		user:     user,
		password: password,
		database: database,
	}, nil
}
