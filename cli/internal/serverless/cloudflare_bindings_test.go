package serverless

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/Golangcodes/nextdeploy/shared/config"

	"github.com/cloudflare/cloudflare-go/v6/workers"
)

func TestBuildScriptMetadata_PesastreamShape(t *testing.T) {
	cf := &config.CloudflareConfig{
		CompatibilityDate:  "2025-09-01",
		CompatibilityFlags: []string{"nodejs_compat_v2", "no_global_navigator"},
		Bindings: &config.CFBindings{
			R2: []config.CFR2Binding{
				{Name: "ASSETS", Bucket: "pesastream-assets"},
				{Name: "MEDIA", Bucket: "pesastream-media"},
			},
			Hyperdrive: []config.CFHyperdriveBinding{
				{Name: "HYPERDRIVE_DB", ID: "abc-def-123"},
			},
			Queues: &config.CFQueueBindings{
				Producers: []config.CFQueueProducer{
					{Name: "WHATSAPP_QUEUE", Queue: "whatsapp-inbound"},
				},
			},
			Vectorize: []config.CFVectorizeBinding{
				{Name: "MEMORY_INDEX", Index: "openclaw-memory"},
			},
			AI: []config.CFAIBinding{
				{Name: "AI"},
			},
			DurableObjects: []config.CFDOBinding{
				{Name: "SESSION_DO", ClassName: "SessionDurableObject"},
			},
			KV: []config.CFKVBinding{
				{Name: "CACHE", NamespaceID: "ns-uuid-1"},
			},
			PlainText: []config.CFPlainTextBinding{
				{Name: "ENVIRONMENT", Value: "production"},
			},
		},
		Migrations: []config.CFMigration{
			{Tag: "v1", NewSQLiteClasses: []string{"SessionDurableObject"}},
			{Tag: "v2", RenamedClasses: []config.CFRenamedDO{{From: "OldName", To: "NewName"}}},
		},
	}

	meta, err := buildScriptMetadata(cf, "default-bucket", "worker.mjs", noResolver)
	if err != nil {
		t.Fatalf("buildScriptMetadata: %v", err)
	}

	// Marshal the actual JSON wire format the SDK will send to CF — that's
	// the contract that matters, not the struct shape.
	rawMeta, err := json.Marshal(meta)
	if err != nil {
		t.Fatalf("marshal meta: %v", err)
	}
	got := string(rawMeta)

	mustContain := []string{
		`"main_module":"worker.mjs"`,
		`"compatibility_date":"2025-09-01"`,
		`"compatibility_flags":["nodejs_compat_v2","no_global_navigator"]`,
		`"name":"ASSETS"`, `"bucket_name":"pesastream-assets"`,
		`"name":"MEDIA"`, `"bucket_name":"pesastream-media"`,
		`"name":"HYPERDRIVE_DB"`, `"id":"abc-def-123"`, `"type":"hyperdrive"`,
		`"name":"WHATSAPP_QUEUE"`, `"queue_name":"whatsapp-inbound"`, `"type":"queue"`,
		`"name":"MEMORY_INDEX"`, `"index_name":"openclaw-memory"`, `"type":"vectorize"`,
		`"name":"AI"`, `"type":"ai"`,
		`"name":"SESSION_DO"`, `"class_name":"SessionDurableObject"`, `"type":"durable_object_namespace"`,
		`"name":"CACHE"`, `"namespace_id":"ns-uuid-1"`, `"type":"kv_namespace"`,
		`"name":"ENVIRONMENT"`, `"text":"production"`, `"type":"plain_text"`,
		// Migrations: multi-step with new_tag pinned to last entry, two steps
		`"new_tag":"v2"`,
		`"new_sqlite_classes":["SessionDurableObject"]`,
		`"renamed_classes":[{"from":"OldName","to":"NewName"}]`,
	}
	for _, frag := range mustContain {
		if !strings.Contains(got, frag) {
			t.Errorf("metadata JSON missing fragment %q\n--- got ---\n%s", frag, got)
		}
	}
}

func TestBuildScriptMetadata_DefaultsAppliedWhenCloudflareBlockNil(t *testing.T) {
	meta, err := buildScriptMetadata(nil, "auto-bucket", "worker.mjs", noResolver)
	if err != nil {
		t.Fatalf("buildScriptMetadata: %v", err)
	}
	rawMeta, _ := json.Marshal(meta)
	got := string(rawMeta)
	for _, frag := range []string{
		`"compatibility_date":"2025-04-01"`,
		`"compatibility_flags":["nodejs_compat_v2"]`,
		`"name":"ASSETS"`, `"bucket_name":"auto-bucket"`,
	} {
		if !strings.Contains(got, frag) {
			t.Errorf("default metadata JSON missing fragment %q\n--- got ---\n%s", frag, got)
		}
	}
}

func TestBuildScriptMetadata_UserR2OverridesAutoAssets(t *testing.T) {
	cf := &config.CloudflareConfig{
		Bindings: &config.CFBindings{
			R2: []config.CFR2Binding{{Name: "STATIC", Bucket: "user-bucket"}},
		},
	}
	meta, err := buildScriptMetadata(cf, "auto-bucket", "worker.mjs", noResolver)
	if err != nil {
		t.Fatalf("buildScriptMetadata: %v", err)
	}
	rawMeta, _ := json.Marshal(meta)
	got := string(rawMeta)
	if strings.Contains(got, `"name":"ASSETS"`) {
		t.Errorf("user R2 binding declared but auto ASSETS still injected:\n%s", got)
	}
	if !strings.Contains(got, `"name":"STATIC"`) || !strings.Contains(got, `"bucket_name":"user-bucket"`) {
		t.Errorf("user R2 binding missing:\n%s", got)
	}
}

func TestBuildScriptMetadata_HyperdriveRefUnresolvedErrors(t *testing.T) {
	cf := &config.CloudflareConfig{
		Bindings: &config.CFBindings{
			Hyperdrive: []config.CFHyperdriveBinding{
				{Name: "DB", Ref: "pesastream-db"},
			},
		},
	}
	_, err := buildScriptMetadata(cf, "bucket", "worker.mjs", noResolver)
	if err == nil {
		t.Fatal("expected error for ref with no resolver match")
	}
	if !strings.Contains(err.Error(), "ref \"pesastream-db\" not found") {
		t.Errorf("wrong error: %v", err)
	}
}

func TestBuildScriptMetadata_HyperdriveRefResolvedToID(t *testing.T) {
	cf := &config.CloudflareConfig{
		Bindings: &config.CFBindings{
			Hyperdrive: []config.CFHyperdriveBinding{
				{Name: "DB", Ref: "pesastream-db"},
			},
		},
	}
	resolver := func(kind, name string) (string, bool) {
		if kind == "hyperdrive" && name == "pesastream-db" {
			return "uuid-from-iac-layer", true
		}
		return "", false
	}
	meta, err := buildScriptMetadata(cf, "bucket", "worker.mjs", resolver)
	if err != nil {
		t.Fatalf("buildScriptMetadata: %v", err)
	}
	raw, _ := json.Marshal(meta)
	if !strings.Contains(string(raw), `"id":"uuid-from-iac-layer"`) {
		t.Errorf("ref→id resolution missing in upload metadata:\n%s", raw)
	}
}

// Sanity check: the SDK's SingleStep migration shape carries the body of
// each migration step. We don't introspect the union directly; the JSON
// round-trip in PesastreamShape covers the wire contract.
var _ = workers.ScriptUpdateParamsMetadataMigrationsWorkersMultipleStepMigrations{}

func TestZoneNameFromHostname(t *testing.T) {
	cases := map[string]string{
		"example.com":          "example.com",
		"sub.example.com":      "example.com",
		"deep.sub.example.com": "example.com",
		"a.b.c.example.com":    "example.com",
		"pesastream.com":       "pesastream.com",
		"app.pesastream.com":   "pesastream.com",
	}
	for in, want := range cases {
		if got := zoneNameFromHostname(in); got != want {
			t.Errorf("zoneNameFromHostname(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestZoneNameFromPattern(t *testing.T) {
	cases := map[string]string{
		"example.com/*":         "example.com",
		"*.example.com/*":       "example.com",
		"app.example.com/api/*": "example.com",
		"pesastream.com/":       "pesastream.com",
		"*.pesastream.com/*":    "pesastream.com",
	}
	for in, want := range cases {
		if got := zoneNameFromPattern(in); got != want {
			t.Errorf("zoneNameFromPattern(%q) = %q, want %q", in, got, want)
		}
	}
}
