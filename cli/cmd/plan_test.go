package cmd

import (
	"testing"

	"github.com/Golangcodes/nextdeploy/cli/internal/serverless"
)

func TestFilterPlanByModules(t *testing.T) {
	full := &serverless.PlanResult{Items: []serverless.PlanItem{
		{Kind: "hyperdrive", Name: "db", Action: serverless.PlanCreate},
		{Kind: "queue", Name: "q1", Action: serverless.PlanNoOp},
		{Kind: "vectorize", Name: "v1", Action: serverless.PlanCreate},
		{Kind: "ai_gateway", Name: "g1", Action: serverless.PlanNoOp},
		{Kind: "dns", Name: "A/api.example.com", Action: serverless.PlanUpdate},
	}}

	cases := []struct {
		name      string
		only      string
		wantCount int
		wantKinds []string
		wantErr   bool
	}{
		{"empty passes through", "", 5, nil, false},
		{"dataplane only", "dataplane", 4, []string{"hyperdrive", "queue", "vectorize", "ai_gateway"}, false},
		{"edge only", "edge", 1, []string{"dns"}, false},
		{"both modules", "dataplane,edge", 5, nil, false},
		{"whitespace + case", " Dataplane , EDGE ", 5, nil, false},
		{"unknown module", "frontend", 0, nil, true},
		{"one valid one invalid", "dataplane,frontend", 0, nil, true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := filterPlanByModules(full, tc.only)
			if tc.wantErr {
				if err == nil {
					t.Fatal("want error, got nil")
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if len(got.Items) != tc.wantCount {
				t.Errorf("got %d items, want %d", len(got.Items), tc.wantCount)
			}
			if tc.wantKinds != nil {
				for i, want := range tc.wantKinds {
					if got.Items[i].Kind != want {
						t.Errorf("item %d: got kind %q, want %q", i, got.Items[i].Kind, want)
					}
				}
			}
		})
	}
}

// TestKindToModule_AllPlanKindsCovered guards against adding a new resource
// kind to Plan() but forgetting to register it in kindToModule — without this
// guard, `--only dataplane` would silently drop the new resource.
func TestKindToModule_AllPlanKindsCovered(t *testing.T) {
	knownKinds := []string{"hyperdrive", "queue", "vectorize", "ai_gateway", "dns"}
	for _, k := range knownKinds {
		if _, ok := kindToModule[k]; !ok {
			t.Errorf("kind %q is emitted by Plan() but missing from kindToModule — --only would silently drop it", k)
		}
	}
}
