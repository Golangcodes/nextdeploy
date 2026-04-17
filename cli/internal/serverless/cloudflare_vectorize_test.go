package serverless

import (
	"strings"
	"testing"

	"github.com/cloudflare/cloudflare-go/v6/vectorize"
)

func TestNormalizeVectorizeMetric_Accepted(t *testing.T) {
	cases := []struct {
		in   string
		want vectorize.IndexDimensionConfigurationMetric
	}{
		{"cosine", vectorize.IndexDimensionConfigurationMetricCosine},
		{"COSINE", vectorize.IndexDimensionConfigurationMetricCosine},
		{" cosine ", vectorize.IndexDimensionConfigurationMetricCosine},
		{"euclidean", vectorize.IndexDimensionConfigurationMetricEuclidean},
		{"dot-product", vectorize.IndexDimensionConfigurationMetricDOTProduct},
		{"dot_product", vectorize.IndexDimensionConfigurationMetricDOTProduct},
		{"dotproduct", vectorize.IndexDimensionConfigurationMetricDOTProduct},
	}
	for _, tc := range cases {
		t.Run(tc.in, func(t *testing.T) {
			got, err := normalizeVectorizeMetric(tc.in)
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if got != tc.want {
				t.Errorf("got %q, want %q", got, tc.want)
			}
		})
	}
}

func TestNormalizeVectorizeMetric_Rejected(t *testing.T) {
	cases := []string{"", "manhattan", "l2", "cosin"}
	for _, in := range cases {
		t.Run(in, func(t *testing.T) {
			_, err := normalizeVectorizeMetric(in)
			if err == nil {
				t.Fatalf("want error for %q, got nil", in)
			}
			if !strings.Contains(err.Error(), "unknown metric") {
				t.Errorf("want 'unknown metric' in error, got %q", err.Error())
			}
		})
	}
}
