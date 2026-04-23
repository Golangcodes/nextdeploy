package serverless

import (
	"context"
	"errors"
	"fmt"
	"net/http"

	"github.com/Golangcodes/nextdeploy/shared/config"

	"github.com/cloudflare/cloudflare-go/v6"
	"github.com/cloudflare/cloudflare-go/v6/ai_gateway"
)

// ensureAIGateway creates an AI Gateway with the given slug if it doesn't
// already exist. If it exists, returns the slug unchanged — we don't reconcile
// gateway settings (cache TTL, rate limits, etc.) because those are typically
// tuned by hand in the CF dashboard and we don't want declarations to clobber
// operator overrides. Schema is intentionally slug-only for this reason.
//
// Returns the slug (gateways are addressed by slug, not UUID).
func (p *CloudflareProvider) ensureAIGateway(ctx context.Context, decl config.CFAIGatewayResource) (string, error) {
	if decl.Slug == "" {
		return "", errors.New("ai_gateway: slug is required")
	}

	_, err := p.cf.AIGateway.Get(ctx, decl.Slug, ai_gateway.AIGatewayGetParams{
		AccountID: cloudflare.F(p.accountID),
	})
	if err == nil {
		p.log.Info("AI Gateway already exists: %s", decl.Slug)
		return decl.Slug, nil
	}

	var apiErr *cloudflare.Error
	if !errors.As(err, &apiErr) || apiErr.StatusCode != http.StatusNotFound {
		return "", fmt.Errorf("ai_gateway %q: get: %w", decl.Slug, err)
	}

	created, err := p.cf.AIGateway.New(ctx, ai_gateway.AIGatewayNewParams{
		AccountID:               cloudflare.F(p.accountID),
		ID:                      cloudflare.F(decl.Slug),
		CacheInvalidateOnUpdate: cloudflare.F(false),
		CacheTTL:                cloudflare.F(int64(0)),
		CollectLogs:             cloudflare.F(true),
		RateLimitingInterval:    cloudflare.F(int64(0)),
		RateLimitingLimit:       cloudflare.F(int64(0)),
	})
	if err != nil {
		return "", fmt.Errorf("ai_gateway %q: create: %w", decl.Slug, err)
	}
	p.log.Info("AI Gateway created: %s", created.ID)
	return created.ID, nil
}
