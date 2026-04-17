package serverless

import (
	"strings"
	"testing"
)

func TestDNSRecordFQDN(t *testing.T) {
	cases := []struct {
		name string
		zone string
		want string
	}{
		{"@", "example.com", "example.com"},
		{"*", "example.com", "*.example.com"},
		{"api", "example.com", "api.example.com"},
		{"api.example.com", "example.com", "api.example.com"},
		{"example.com", "example.com", "example.com"},
		{"deep.sub.example.com", "example.com", "deep.sub.example.com"},
	}
	for _, tc := range cases {
		t.Run(tc.name+"@"+tc.zone, func(t *testing.T) {
			if got := dnsRecordFQDN(tc.name, tc.zone); got != tc.want {
				t.Errorf("got %q, want %q", got, tc.want)
			}
		})
	}
}

func TestBuildDNSRecordBody_Supported(t *testing.T) {
	cases := []string{"A", "AAAA", "CNAME", "TXT"}
	for _, recType := range cases {
		t.Run(recType, func(t *testing.T) {
			n, e, err := buildDNSRecordBody("api.example.com", recType, "1.2.3.4", 300, false)
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if n == nil || e == nil {
				t.Errorf("expected non-nil bodies, got new=%v edit=%v", n, e)
			}
		})
	}
}

func TestBuildDNSRecordBody_Unsupported(t *testing.T) {
	_, _, err := buildDNSRecordBody("api.example.com", "SRV", "x", 300, false)
	if err == nil {
		t.Fatal("want error for SRV, got nil")
	}
	if !strings.Contains(err.Error(), "unsupported DNS record type") {
		t.Errorf("want 'unsupported DNS record type' in error, got %q", err.Error())
	}
}
