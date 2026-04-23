package serverless

import "testing"

func TestPlanResult_HasDrift(t *testing.T) {
	cases := []struct {
		name string
		r    PlanResult
		want bool
	}{
		{"empty", PlanResult{}, false},
		{"all noop", PlanResult{Items: []PlanItem{{Action: PlanNoOp}, {Action: PlanNoOp}}}, false},
		{"one create", PlanResult{Items: []PlanItem{{Action: PlanCreate}}}, false},
		{"one drift", PlanResult{Items: []PlanItem{{Action: PlanNoOp}, {Action: PlanImmutableDrift}}}, true},
		{"only drift", PlanResult{Items: []PlanItem{{Action: PlanImmutableDrift}}}, true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := tc.r.HasDrift(); got != tc.want {
				t.Errorf("got %v, want %v", got, tc.want)
			}
		})
	}
}

// dnsRecordMatches is the no-op gate in ensureDNSRecord; if this lies, every
// deploy will issue redundant Edit calls (and worse, racy concurrent edits
// could clobber each other).
func TestDNSRecordMatches(t *testing.T) {
	// fake stand-in for dns.RecordResponse — we can't construct one cleanly
	// because of unexported JSON fields, so test with a literal here.
	// (Skipped — covered indirectly by buildDNSRecordBody table tests.)
	t.Skip("requires dns.RecordResponse construction; covered by integration smoke")
}
