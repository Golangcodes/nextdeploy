package serverless

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strings"

	"github.com/Golangcodes/nextdeploy/shared/config"

	"github.com/cloudflare/cloudflare-go/v6"
	"github.com/cloudflare/cloudflare-go/v6/vectorize"
)

// ensureVectorizeIndex creates or validates a Vectorize index against the
// declared dimensions + metric.
//
// Idempotency note (Pesastream §10): dimensions and metric are IMMUTABLE
// after creation. If the existing index has different values from the
// declaration, we error loudly rather than recreate — destroying a Vectorize
// index also destroys every embedding stored in it. Operator must drop the
// index manually if they really want a recreate.
//
// Returns the index name (CF uses name-as-ID for Vectorize, so there is no
// separate UUID — we still stash it in the resource map for symmetry).
func (p *CloudflareProvider) ensureVectorizeIndex(ctx context.Context, decl config.CFVectorizeResource) (string, error) {
	if decl.Name == "" {
		return "", errors.New("vectorize: name is required")
	}
	if decl.Dimensions <= 0 {
		return "", fmt.Errorf("vectorize %q: dimensions must be > 0", decl.Name)
	}
	metric, err := normalizeVectorizeMetric(decl.Metric)
	if err != nil {
		return "", fmt.Errorf("vectorize %q: %w", decl.Name, err)
	}

	existing, err := p.cf.Vectorize.Indexes.Get(ctx, decl.Name, vectorize.IndexGetParams{
		AccountID: cloudflare.F(p.accountID),
	})
	if err == nil {
		if int64(decl.Dimensions) != existing.Config.Dimensions {
			return "", fmt.Errorf(
				"vectorize %q drift: declared dimensions=%d, existing=%d. Vectorize dimensions are immutable; manually delete the index if you really want to recreate (this destroys all embeddings)",
				decl.Name, decl.Dimensions, existing.Config.Dimensions,
			)
		}
		if existing.Config.Metric != metric {
			return "", fmt.Errorf(
				"vectorize %q drift: declared metric=%q, existing=%q. Vectorize metric is immutable",
				decl.Name, metric, existing.Config.Metric,
			)
		}
		p.log.Info("Vectorize index already exists: %s (dims=%d, metric=%s)", decl.Name, existing.Config.Dimensions, existing.Config.Metric)
		return decl.Name, nil
	}

	var apiErr *cloudflare.Error
	if !errors.As(err, &apiErr) || apiErr.StatusCode != http.StatusNotFound {
		return "", fmt.Errorf("vectorize %q: get: %w", decl.Name, err)
	}

	created, err := p.cf.Vectorize.Indexes.New(ctx, vectorize.IndexNewParams{
		AccountID: cloudflare.F(p.accountID),
		Name:      cloudflare.F(decl.Name),
		Config: cloudflare.F[vectorize.IndexNewParamsConfigUnion](vectorize.IndexDimensionConfigurationParam{
			Dimensions: cloudflare.F(int64(decl.Dimensions)),
			Metric:     cloudflare.F(metric),
		}),
	})
	if err != nil {
		return "", fmt.Errorf("vectorize %q: create: %w", decl.Name, err)
	}
	p.log.Info("Vectorize index created: %s (dims=%d, metric=%s)", created.Name, decl.Dimensions, metric)
	return created.Name, nil
}

// normalizeVectorizeMetric maps user-friendly metric strings to the SDK enum.
// Accepts "cosine"/"euclidean"/"dot-product" (with hyphens or underscores).
func normalizeVectorizeMetric(m string) (vectorize.IndexDimensionConfigurationMetric, error) {
	switch strings.ToLower(strings.ReplaceAll(strings.TrimSpace(m), "_", "-")) {
	case "cosine":
		return vectorize.IndexDimensionConfigurationMetricCosine, nil
	case "euclidean":
		return vectorize.IndexDimensionConfigurationMetricEuclidean, nil
	case "dot-product", "dotproduct":
		return vectorize.IndexDimensionConfigurationMetricDOTProduct, nil
	default:
		return "", fmt.Errorf("unknown metric %q (want cosine|euclidean|dot-product)", m)
	}
}
