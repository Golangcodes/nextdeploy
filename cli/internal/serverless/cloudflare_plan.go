package serverless

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strings"

	"github.com/Golangcodes/nextdeploy/shared/config"

	"github.com/cloudflare/cloudflare-go/v6"
	"github.com/cloudflare/cloudflare-go/v6/ai_gateway"
	"github.com/cloudflare/cloudflare-go/v6/vectorize"
)

// PlanAction is the verdict the diff engine emits for each declared resource.
// "immutable-drift" is its own action (vs. error) so the renderer can surface
// every problem in one pass instead of bailing on the first immutable mismatch.
type PlanAction string

const (
	PlanCreate         PlanAction = "create"
	PlanUpdate         PlanAction = "update"
	PlanNoOp           PlanAction = "no-op"
	PlanImmutableDrift PlanAction = "immutable-drift"
)

// PlanItem is one row in the plan output. Detail is human-readable; for NoOp
// it is empty, for Update it describes the field-level diff, for Drift it
// explains the immutable mismatch.
type PlanItem struct {
	Kind   string
	Name   string
	Action PlanAction
	Detail string
}

// PlanResult is the full diff bundle. HasDrift() flags whether any item is
// ImmutableDrift so the cmd can exit non-zero (CI-friendly).
type PlanResult struct {
	Items []PlanItem
}

func (r *PlanResult) HasDrift() bool {
	for _, it := range r.Items {
		if it.Action == PlanImmutableDrift {
			return true
		}
	}
	return false
}

// Plan computes a dry-run diff for every IaC resource declared under
// cfg.Serverless.Cloudflare.Resources. Read-only: makes GET / List calls
// against the CF API but no mutations. Caller must have already called
// Initialize() so p.cf is wired.
//
// Scope: hyperdrive, queues, vectorize, ai_gateway, dns. Custom domains and
// routes are tied to worker upload (not standalone resources) and so are not
// planned here — they're computed during DeployCompute.
func (p *CloudflareProvider) Plan(ctx context.Context, cfg *config.NextDeployConfig) (*PlanResult, error) {
	if cfg.Serverless == nil || cfg.Serverless.Cloudflare == nil || cfg.Serverless.Cloudflare.Resources == nil {
		return &PlanResult{}, nil
	}
	res := cfg.Serverless.Cloudflare.Resources
	out := &PlanResult{}

	for _, h := range res.Hyperdrive {
		item, err := p.planHyperdrive(ctx, h)
		if err != nil {
			return nil, fmt.Errorf("plan hyperdrive %q: %w", h.Name, err)
		}
		out.Items = append(out.Items, item)
	}

	for _, q := range res.Queues {
		item, err := p.planQueue(ctx, q)
		if err != nil {
			return nil, fmt.Errorf("plan queue %q: %w", q.Name, err)
		}
		out.Items = append(out.Items, item)
	}

	for _, v := range res.Vectorize {
		item, err := p.planVectorize(ctx, v)
		if err != nil {
			return nil, fmt.Errorf("plan vectorize %q: %w", v.Name, err)
		}
		out.Items = append(out.Items, item)
	}

	for _, g := range res.AIGateway {
		item, err := p.planAIGateway(ctx, g)
		if err != nil {
			return nil, fmt.Errorf("plan ai_gateway %q: %w", g.Slug, err)
		}
		out.Items = append(out.Items, item)
	}

	for _, d := range res.DNS {
		item, err := p.planDNSRecord(ctx, d)
		if err != nil {
			return nil, fmt.Errorf("plan dns %s/%s: %w", d.Type, d.Name, err)
		}
		out.Items = append(out.Items, item)
	}

	return out, nil
}

func (p *CloudflareProvider) planHyperdrive(ctx context.Context, decl config.CFHyperdriveResource) (PlanItem, error) {
	id, err := p.findHyperdriveID(ctx, decl.Name)
	if err != nil {
		return PlanItem{}, err
	}
	if id == "" {
		return PlanItem{Kind: "hyperdrive", Name: decl.Name, Action: PlanCreate}, nil
	}
	// Hyperdrive supports hot-swap on origin fields, so existing always
	// counts as Update (we don't compare individual fields because the CF
	// API never returns the password — comparing host/port alone would lie).
	return PlanItem{Kind: "hyperdrive", Name: decl.Name, Action: PlanUpdate, Detail: "origin will be re-applied (CF never returns password, can't compare)"}, nil
}

func (p *CloudflareProvider) planQueue(ctx context.Context, decl config.CFQueueResource) (PlanItem, error) {
	id, err := p.findQueueID(ctx, decl.Name)
	if err != nil {
		return PlanItem{}, err
	}
	if id == "" {
		return PlanItem{Kind: "queue", Name: decl.Name, Action: PlanCreate}, nil
	}
	return PlanItem{Kind: "queue", Name: decl.Name, Action: PlanNoOp}, nil
}

func (p *CloudflareProvider) planVectorize(ctx context.Context, decl config.CFVectorizeResource) (PlanItem, error) {
	if decl.Dimensions <= 0 {
		return PlanItem{}, fmt.Errorf("dimensions must be > 0")
	}
	metric, err := normalizeVectorizeMetric(decl.Metric)
	if err != nil {
		return PlanItem{}, err
	}

	existing, err := p.cf.Vectorize.Indexes.Get(ctx, decl.Name, vectorize.IndexGetParams{
		AccountID: cloudflare.F(p.accountID),
	})
	if err != nil {
		var apiErr *cloudflare.Error
		if errors.As(err, &apiErr) && apiErr.StatusCode == http.StatusNotFound {
			return PlanItem{Kind: "vectorize", Name: decl.Name, Action: PlanCreate, Detail: fmt.Sprintf("dims=%d metric=%s", decl.Dimensions, metric)}, nil
		}
		return PlanItem{}, err
	}

	var drifts []string
	if int64(decl.Dimensions) != existing.Config.Dimensions {
		drifts = append(drifts, fmt.Sprintf("dimensions: declared=%d existing=%d", decl.Dimensions, existing.Config.Dimensions))
	}
	if existing.Config.Metric != metric {
		drifts = append(drifts, fmt.Sprintf("metric: declared=%q existing=%q", metric, existing.Config.Metric))
	}
	if len(drifts) > 0 {
		return PlanItem{
			Kind:   "vectorize",
			Name:   decl.Name,
			Action: PlanImmutableDrift,
			Detail: strings.Join(drifts, "; ") + " (immutable; manual delete required)",
		}, nil
	}
	return PlanItem{Kind: "vectorize", Name: decl.Name, Action: PlanNoOp}, nil
}

func (p *CloudflareProvider) planAIGateway(ctx context.Context, decl config.CFAIGatewayResource) (PlanItem, error) {
	if decl.Slug == "" {
		return PlanItem{}, errors.New("slug is required")
	}
	_, err := p.cf.AIGateway.Get(ctx, decl.Slug, ai_gateway.AIGatewayGetParams{
		AccountID: cloudflare.F(p.accountID),
	})
	if err == nil {
		return PlanItem{Kind: "ai_gateway", Name: decl.Slug, Action: PlanNoOp}, nil
	}
	var apiErr *cloudflare.Error
	if errors.As(err, &apiErr) && apiErr.StatusCode == http.StatusNotFound {
		return PlanItem{Kind: "ai_gateway", Name: decl.Slug, Action: PlanCreate}, nil
	}
	return PlanItem{}, err
}

func (p *CloudflareProvider) planDNSRecord(ctx context.Context, decl config.CFDNSRecord) (PlanItem, error) {
	if decl.Zone == "" || decl.Name == "" || decl.Type == "" || decl.Content == "" {
		return PlanItem{}, errors.New("zone, name, type, content are required")
	}
	zoneID, err := p.getZoneID(ctx, decl.Zone)
	if err != nil {
		return PlanItem{}, err
	}
	fqdn := dnsRecordFQDN(decl.Name, decl.Zone)
	recType := strings.ToUpper(decl.Type)
	ttl := decl.TTL
	if ttl <= 0 {
		ttl = 1
	}

	existing, err := p.findDNSRecord(ctx, zoneID, fqdn, recType)
	if err != nil {
		return PlanItem{}, err
	}
	itemName := fmt.Sprintf("%s/%s", recType, fqdn)
	if existing == nil {
		return PlanItem{Kind: "dns", Name: itemName, Action: PlanCreate, Detail: fmt.Sprintf("→ %s (ttl=%d, proxied=%v)", decl.Content, ttl, decl.Proxied)}, nil
	}
	if dnsRecordMatches(*existing, decl.Content, ttl, decl.Proxied) {
		return PlanItem{Kind: "dns", Name: itemName, Action: PlanNoOp}, nil
	}
	var diffs []string
	if existing.Content != decl.Content {
		diffs = append(diffs, fmt.Sprintf("content: %q → %q", existing.Content, decl.Content))
	}
	if int(existing.TTL) != ttl {
		diffs = append(diffs, fmt.Sprintf("ttl: %d → %d", int(existing.TTL), ttl))
	}
	if existing.Proxied != decl.Proxied {
		diffs = append(diffs, fmt.Sprintf("proxied: %v → %v", existing.Proxied, decl.Proxied))
	}
	return PlanItem{Kind: "dns", Name: itemName, Action: PlanUpdate, Detail: strings.Join(diffs, ", ")}, nil
}
