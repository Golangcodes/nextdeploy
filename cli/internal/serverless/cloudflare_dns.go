package serverless

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/Golangcodes/nextdeploy/shared/config"

	"github.com/cloudflare/cloudflare-go/v6"
	"github.com/cloudflare/cloudflare-go/v6/dns"
)

// ensureDNSRecord creates a DNS record if one doesn't already exist for
// (zone, name, type), or updates it in-place if content/ttl/proxied drift.
//
// Match key is (name, type) — CF allows multiple records of different types
// on the same name (e.g. A + AAAA), but we treat (name,type) as the unique
// identifier for declarative purposes. If you need multiple A records on the
// same name (round-robin), declare them outside NextDeploy.
func (p *CloudflareProvider) ensureDNSRecord(ctx context.Context, decl config.CFDNSRecord) error {
	if decl.Zone == "" {
		return errors.New("dns: zone is required")
	}
	if decl.Name == "" {
		return errors.New("dns: name is required")
	}
	if decl.Type == "" {
		return errors.New("dns: type is required")
	}
	if decl.Content == "" {
		return fmt.Errorf("dns %s/%s: content is required", decl.Type, decl.Name)
	}

	zoneID, err := p.getZoneID(ctx, decl.Zone)
	if err != nil {
		return fmt.Errorf("dns %s %s: resolve zone %q: %w", decl.Type, decl.Name, decl.Zone, err)
	}

	fqdn := dnsRecordFQDN(decl.Name, decl.Zone)
	recType := strings.ToUpper(decl.Type)
	ttl := decl.TTL
	if ttl <= 0 {
		ttl = 1 // CF "automatic"
	}

	newBody, editBody, err := buildDNSRecordBody(fqdn, recType, decl.Content, ttl, decl.Proxied)
	if err != nil {
		return fmt.Errorf("dns %s %s: %w", recType, fqdn, err)
	}

	existing, err := p.findDNSRecord(ctx, zoneID, fqdn, recType)
	if err != nil {
		return fmt.Errorf("dns %s %s: list: %w", recType, fqdn, err)
	}
	if existing != nil {
		if dnsRecordMatches(*existing, decl.Content, ttl, decl.Proxied) {
			p.log.Info("DNS record already current: %s %s → %s", recType, fqdn, decl.Content)
			return nil
		}
		_, err := p.cf.DNS.Records.Edit(ctx, existing.ID, dns.RecordEditParams{
			ZoneID: cloudflare.F(zoneID),
			Body:   editBody,
		})
		if err != nil {
			return fmt.Errorf("dns %s %s: update: %w", recType, fqdn, err)
		}
		p.log.Info("DNS record updated: %s %s → %s (ttl=%d, proxied=%v)", recType, fqdn, decl.Content, ttl, decl.Proxied)
		return nil
	}

	if _, err := p.cf.DNS.Records.New(ctx, dns.RecordNewParams{
		ZoneID: cloudflare.F(zoneID),
		Body:   newBody,
	}); err != nil {
		return fmt.Errorf("dns %s %s: create: %w", recType, fqdn, err)
	}
	p.log.Info("DNS record created: %s %s → %s (ttl=%d, proxied=%v)", recType, fqdn, decl.Content, ttl, decl.Proxied)
	return nil
}

// findDNSRecord returns the first record matching (name, type) within the
// zone, or nil if none exists.
func (p *CloudflareProvider) findDNSRecord(ctx context.Context, zoneID, fqdn, recType string) (*dns.RecordResponse, error) {
	iter := p.cf.DNS.Records.ListAutoPaging(ctx, dns.RecordListParams{
		ZoneID: cloudflare.F(zoneID),
		Name: cloudflare.F(dns.RecordListParamsName{
			Exact: cloudflare.F(fqdn),
		}),
	})
	for iter.Next() {
		r := iter.Current()
		if string(r.Type) == recType {
			return &r, nil
		}
	}
	if err := iter.Err(); err != nil {
		return nil, err
	}
	return nil, nil
}

// dnsRecordFQDN expands "@" → zone, "*" → "*.zone", and "sub" → "sub.zone".
// Names that already end in the zone (or contain a dot) are left alone.
func dnsRecordFQDN(name, zone string) string {
	switch name {
	case "@":
		return zone
	case "*":
		return "*." + zone
	}
	if strings.HasSuffix(name, "."+zone) || name == zone {
		return name
	}
	if strings.Contains(name, ".") {
		// already an FQDN (e.g. user typed "api.example.com"); trust it
		return name
	}
	return name + "." + zone
}

// dnsRecordMatches reports whether the existing CF record already has the
// declared content/ttl/proxied values — used to skip no-op updates.
func dnsRecordMatches(r dns.RecordResponse, content string, ttl int, proxied bool) bool {
	return r.Content == content && int(r.TTL) == ttl && r.Proxied == proxied
}

// buildDNSRecordBody returns the right type-specific param body for the
// supported record types. The same struct value satisfies both
// RecordNewParamsBodyUnion and RecordEditParamsBodyUnion (the SDK uses two
// sentinel interfaces but the same set of types implements both), so we
// return it twice rather than duplicate the switch.
func buildDNSRecordBody(name, recType, content string, ttl int, proxied bool) (dns.RecordNewParamsBodyUnion, dns.RecordEditParamsBodyUnion, error) {
	switch recType {
	case "A":
		v := dns.ARecordParam{
			Name:    cloudflare.F(name),
			Type:    cloudflare.F(dns.ARecordTypeA),
			Content: cloudflare.F(content),
			TTL:     cloudflare.F(dns.TTL(ttl)),
			Proxied: cloudflare.F(proxied),
		}
		return v, v, nil
	case "AAAA":
		v := dns.AAAARecordParam{
			Name:    cloudflare.F(name),
			Type:    cloudflare.F(dns.AAAARecordTypeAAAA),
			Content: cloudflare.F(content),
			TTL:     cloudflare.F(dns.TTL(ttl)),
			Proxied: cloudflare.F(proxied),
		}
		return v, v, nil
	case "CNAME":
		v := dns.CNAMERecordParam{
			Name:    cloudflare.F(name),
			Type:    cloudflare.F(dns.CNAMERecordTypeCNAME),
			Content: cloudflare.F(content),
			TTL:     cloudflare.F(dns.TTL(ttl)),
			Proxied: cloudflare.F(proxied),
		}
		return v, v, nil
	case "TXT":
		v := dns.TXTRecordParam{
			Name:    cloudflare.F(name),
			Type:    cloudflare.F(dns.TXTRecordTypeTXT),
			Content: cloudflare.F(content),
			TTL:     cloudflare.F(dns.TTL(ttl)),
		}
		return v, v, nil
	default:
		return nil, nil, fmt.Errorf("unsupported DNS record type %q (supported: A, AAAA, CNAME, TXT)", recType)
	}
}
