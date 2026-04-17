package serverless

import (
	"context"
	"fmt"
	"sync"

	"github.com/Golangcodes/nextdeploy/shared/config"
)

// resourceMap holds the IDs of resources provisioned by ensure* methods so
// later code (binding metadata, plan diffs) can resolve `ref: <name>` to a
// real CF UUID. Keys are namespaced as "<kind>:<name>" (e.g.
// "hyperdrive:pesastream-db") so different resource types can share a name.
type resourceMap struct {
	mu sync.RWMutex
	m  map[string]string
}

func newResourceMap() *resourceMap {
	return &resourceMap{m: map[string]string{}}
}

func (r *resourceMap) set(kind, name, id string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.m[kind+":"+name] = id
}

func (r *resourceMap) get(kind, name string) (string, bool) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	id, ok := r.m[kind+":"+name]
	return id, ok
}

// ProvisionResources walks cfg.Serverless.Cloudflare.Resources and calls the
// matching ensure* helper for each declared resource. Stops on first error.
// Results are stashed on p.provisioned for binding ref resolution at upload
// time. Safe to call before DeployCompute.
func (p *CloudflareProvider) ProvisionResources(ctx context.Context, cfg *config.NextDeployConfig) error {
	if cfg.Serverless == nil || cfg.Serverless.Cloudflare == nil || cfg.Serverless.Cloudflare.Resources == nil {
		return nil
	}
	res := cfg.Serverless.Cloudflare.Resources
	if p.provisioned == nil {
		p.provisioned = newResourceMap()
	}

	for _, h := range res.Hyperdrive {
		id, err := p.ensureHyperdrive(ctx, h)
		if err != nil {
			return fmt.Errorf("hyperdrive %q: %w", h.Name, err)
		}
		p.provisioned.set("hyperdrive", h.Name, id)
	}

	for _, q := range res.Queues {
		id, err := p.ensureQueue(ctx, q.Name)
		if err != nil {
			return fmt.Errorf("queue %q: %w", q.Name, err)
		}
		p.provisioned.set("queue", q.Name, id)
	}

	for _, v := range res.Vectorize {
		id, err := p.ensureVectorizeIndex(ctx, v)
		if err != nil {
			return fmt.Errorf("vectorize %q: %w", v.Name, err)
		}
		p.provisioned.set("vectorize", v.Name, id)
	}

	for _, g := range res.AIGateway {
		slug, err := p.ensureAIGateway(ctx, g)
		if err != nil {
			return fmt.Errorf("ai_gateway %q: %w", g.Slug, err)
		}
		p.provisioned.set("ai_gateway", g.Slug, slug)
	}

	for _, d := range res.DNS {
		if err := p.ensureDNSRecord(ctx, d); err != nil {
			return fmt.Errorf("dns %s/%s: %w", d.Type, d.Name, err)
		}
	}

	if res.ZoneSettings != nil {
		if err := p.ensureZoneSettings(ctx, *res.ZoneSettings); err != nil {
			return fmt.Errorf("zone_settings: %w", err)
		}
	}

	return nil
}
