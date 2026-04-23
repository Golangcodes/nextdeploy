package serverless

import (
	"context"
	"errors"
	"fmt"

	"github.com/Golangcodes/nextdeploy/shared/config"

	"github.com/cloudflare/cloudflare-go/v6"
	"github.com/cloudflare/cloudflare-go/v6/dns"
)

// ensureZoneSettings applies zone-level overrides for cutover scenarios.
// Currently supports MinTTL: lowers the TTL of every DNS record in the zone
// whose current TTL exceeds MinTTL.
//
// CF does NOT expose a zone-level TTL knob — this is implemented by listing
// all records in the zone and editing each one over its current threshold.
// Records with TTL=1 (CF "automatic") are left alone since the user has
// explicitly opted into CF's adaptive caching.
//
// Because this affects records NOT declared in resources.dns, it's a blunt
// instrument; reserve for cutover prep where you want every record in the
// zone to drop to the same low TTL.
func (p *CloudflareProvider) ensureZoneSettings(ctx context.Context, s config.CFZoneSettings) error {
	if s.Zone == "" {
		return errors.New("zone_settings: zone is required")
	}
	if s.MinTTL <= 0 {
		// nothing to do — settings block exists but no overrides set
		return nil
	}
	if s.MinTTL == 1 {
		return errors.New("zone_settings: min_ttl=1 (auto) is not a valid floor; pick an explicit seconds value")
	}

	zoneID, err := p.getZoneID(ctx, s.Zone)
	if err != nil {
		return fmt.Errorf("zone_settings %q: resolve zone: %w", s.Zone, err)
	}

	target := dns.TTL(s.MinTTL)
	lowered := 0
	iter := p.cf.DNS.Records.ListAutoPaging(ctx, dns.RecordListParams{
		ZoneID: cloudflare.F(zoneID),
	})
	for iter.Next() {
		r := iter.Current()
		if r.TTL <= target || r.TTL == 1 {
			continue
		}
		_, editBody, err := buildDNSRecordBody(r.Name, string(r.Type), r.Content, s.MinTTL, r.Proxied)
		if err != nil {
			// unsupported types (SRV, MX, etc.) are skipped — we don't have a
			// param shape for them yet, and rewriting them would lose data.
			p.log.Info("zone_settings: skipping %s %s (type not supported by TTL override)", r.Type, r.Name)
			continue
		}
		if _, err := p.cf.DNS.Records.Edit(ctx, r.ID, dns.RecordEditParams{
			ZoneID: cloudflare.F(zoneID),
			Body:   editBody,
		}); err != nil {
			return fmt.Errorf("zone_settings %q: lower TTL for %s %s: %w", s.Zone, r.Type, r.Name, err)
		}
		p.log.Info("Zone TTL lowered: %s %s %d → %d", r.Type, r.Name, int(r.TTL), s.MinTTL)
		lowered++
	}
	if err := iter.Err(); err != nil {
		return fmt.Errorf("zone_settings %q: list records: %w", s.Zone, err)
	}
	p.log.Info("Zone TTL pass complete: %s lowered %d record(s) to %ds", s.Zone, lowered, s.MinTTL)
	return nil
}
