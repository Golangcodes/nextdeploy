package serverless

import (
	"fmt"

	"github.com/Golangcodes/nextdeploy/shared/config"

	"github.com/cloudflare/cloudflare-go/v6"
	"github.com/cloudflare/cloudflare-go/v6/workers"
)

// Defaults applied when CloudflareConfig fields are unset.
const (
	defaultCFCompatibilityDate = "2025-04-01"
	defaultR2AssetsBindingName = "ASSETS"
)

var defaultCFCompatibilityFlags = []string{"nodejs_compat_v2"}

// refResolver returns a CF resource UUID for a (kind, name) pair previously
// stashed by ProvisionResources. Returns ("", false) if unknown.
type refResolver func(kind, name string) (string, bool)

// noResolver is used by callers that don't have a provisioned-ID map.
// Bindings with `ref:` will then fail with a clear error.
func noResolver(string, string) (string, bool) { return "", false }

// buildScriptMetadata translates the user's CloudflareConfig + auto-derived
// per-app R2 bucket into a workers.ScriptUpdateParamsMetadata ready to upload.
//
// `defaultBucket` is the auto-derived R2 asset bucket name. If the user did
// not declare any R2 bindings in YAML, we add an ASSETS binding pointing at
// it (preserves existing single-bucket behavior). If they declared their own
// R2 bindings, we trust them — no auto-injection.
//
// `resolveRef` resolves `ref:` shortcuts on bindings (e.g. hyperdrive.ref) to
// the CF UUID stashed by ProvisionResources. Pass noResolver if no IaC layer
// has run yet — bindings using `ref:` will then error.
func buildScriptMetadata(cf *config.CloudflareConfig, defaultBucket, entryName string, resolveRef refResolver) (workers.ScriptUpdateParamsMetadata, error) {
	compatDate := defaultCFCompatibilityDate
	flags := defaultCFCompatibilityFlags
	if cf != nil {
		if cf.CompatibilityDate != "" {
			compatDate = cf.CompatibilityDate
		}
		if len(cf.CompatibilityFlags) > 0 {
			flags = cf.CompatibilityFlags
		}
	}

	bindings, err := buildBindings(cf, defaultBucket, resolveRef)
	if err != nil {
		return workers.ScriptUpdateParamsMetadata{}, err
	}

	meta := workers.ScriptUpdateParamsMetadata{
		MainModule:         cloudflare.F(entryName),
		CompatibilityDate:  cloudflare.F(compatDate),
		CompatibilityFlags: cloudflare.F(flags),
		Bindings:           cloudflare.F(bindings),
	}

	if cf != nil && len(cf.Migrations) > 0 {
		meta.Migrations = cloudflare.F[workers.ScriptUpdateParamsMetadataMigrationsUnion](
			buildMigrations(cf.Migrations),
		)
	}
	return meta, nil
}

func buildBindings(cf *config.CloudflareConfig, defaultBucket string, resolveRef refResolver) ([]workers.ScriptUpdateParamsMetadataBindingUnion, error) {
	if resolveRef == nil {
		resolveRef = noResolver
	}
	var out []workers.ScriptUpdateParamsMetadataBindingUnion

	hasR2 := cf != nil && cf.Bindings != nil && len(cf.Bindings.R2) > 0
	if !hasR2 && defaultBucket != "" {
		out = append(out, workers.ScriptUpdateParamsMetadataBindingsWorkersBindingKindR2Bucket{
			Name:       cloudflare.F(defaultR2AssetsBindingName),
			BucketName: cloudflare.F(defaultBucket),
			Type:       cloudflare.F(workers.ScriptUpdateParamsMetadataBindingsWorkersBindingKindR2BucketTypeR2Bucket),
		})
	}
	if cf == nil || cf.Bindings == nil {
		return out, nil
	}

	b := cf.Bindings

	for _, r2 := range b.R2 {
		bucket := r2.Bucket
		if bucket == "" {
			bucket = defaultBucket
		}
		if bucket == "" {
			return nil, fmt.Errorf("r2 binding %q: bucket name required (no default)", r2.Name)
		}
		out = append(out, workers.ScriptUpdateParamsMetadataBindingsWorkersBindingKindR2Bucket{
			Name:       cloudflare.F(r2.Name),
			BucketName: cloudflare.F(bucket),
			Type:       cloudflare.F(workers.ScriptUpdateParamsMetadataBindingsWorkersBindingKindR2BucketTypeR2Bucket),
		})
	}

	for _, h := range b.Hyperdrive {
		id := h.ID
		if id == "" && h.Ref != "" {
			resolved, ok := resolveRef("hyperdrive", h.Ref)
			if !ok {
				return nil, fmt.Errorf("hyperdrive binding %q: ref %q not found in provisioned resources (run ProvisionResources first, or set id: explicitly)", h.Name, h.Ref)
			}
			id = resolved
		}
		if id == "" {
			return nil, fmt.Errorf("hyperdrive binding %q: id or ref required", h.Name)
		}
		out = append(out, workers.ScriptUpdateParamsMetadataBindingsWorkersBindingKindHyperdrive{
			Name: cloudflare.F(h.Name),
			ID:   cloudflare.F(id),
			Type: cloudflare.F(workers.ScriptUpdateParamsMetadataBindingsWorkersBindingKindHyperdriveTypeHyperdrive),
		})
	}

	if b.Queues != nil {
		for _, p := range b.Queues.Producers {
			out = append(out, workers.ScriptUpdateParamsMetadataBindingsWorkersBindingKindQueue{
				Name:      cloudflare.F(p.Name),
				QueueName: cloudflare.F(p.Queue),
				Type:      cloudflare.F(workers.ScriptUpdateParamsMetadataBindingsWorkersBindingKindQueueTypeQueue),
			})
		}
		// Queue consumers are NOT bindings — they are Worker-level consumer
		// configuration. The SDK exposes them via a separate API surface
		// (Workers.Scripts.Settings.Edit). We do not yet wire that here;
		// users who need consumer wiring should still declare it in YAML so
		// the plan command can surface the gap. TODO: consumer registration.
	}

	for _, v := range b.Vectorize {
		out = append(out, workers.ScriptUpdateParamsMetadataBindingsWorkersBindingKindVectorize{
			Name:      cloudflare.F(v.Name),
			IndexName: cloudflare.F(v.Index),
			Type:      cloudflare.F(workers.ScriptUpdateParamsMetadataBindingsWorkersBindingKindVectorizeTypeVectorize),
		})
	}

	for _, ai := range b.AI {
		// AI binding takes only Name + Type. AI Gateway routing is a runtime
		// option (env.AI.run(..., { gateway: { id } })), not metadata. We
		// keep the YAML field for documentation but don't translate it.
		out = append(out, workers.ScriptUpdateParamsMetadataBindingsWorkersBindingKindAI{
			Name: cloudflare.F(ai.Name),
			Type: cloudflare.F(workers.ScriptUpdateParamsMetadataBindingsWorkersBindingKindAITypeAI),
		})
	}

	for _, do := range b.DurableObjects {
		binding := workers.ScriptUpdateParamsMetadataBindingsWorkersBindingKindDurableObjectNamespace{
			Name:      cloudflare.F(do.Name),
			ClassName: cloudflare.F(do.ClassName),
			Type:      cloudflare.F(workers.ScriptUpdateParamsMetadataBindingsWorkersBindingKindDurableObjectNamespaceTypeDurableObjectNamespace),
		}
		if do.Script != "" {
			binding.ScriptName = cloudflare.F(do.Script)
		}
		out = append(out, binding)
	}

	for _, kv := range b.KV {
		out = append(out, workers.ScriptUpdateParamsMetadataBindingsWorkersBindingKindKVNamespace{
			Name:        cloudflare.F(kv.Name),
			NamespaceID: cloudflare.F(kv.NamespaceID),
			Type:        cloudflare.F(workers.ScriptUpdateParamsMetadataBindingsWorkersBindingKindKVNamespaceTypeKVNamespace),
		})
	}

	for _, pt := range b.PlainText {
		out = append(out, workers.ScriptUpdateParamsMetadataBindingsWorkersBindingKindPlainText{
			Name: cloudflare.F(pt.Name),
			Text: cloudflare.F(pt.Value),
			Type: cloudflare.F(workers.ScriptUpdateParamsMetadataBindingsWorkersBindingKindPlainTextTypePlainText),
		})
	}

	return out, nil
}

// buildMigrations converts the YAML migration list to the SDK multi-step form.
// The last entry's tag becomes new_tag (the latest applied migration). We do
// not set old_tag — the upload then becomes idempotent (CF will skip already-
// applied tags rather than reject the upload). Tag mismatches that the user
// wants enforced are a follow-up.
func buildMigrations(ms []config.CFMigration) workers.ScriptUpdateParamsMetadataMigrationsWorkersMultipleStepMigrations {
	steps := make([]workers.MigrationStepParam, 0, len(ms))
	for _, m := range ms {
		step := workers.MigrationStepParam{}
		if len(m.NewSQLiteClasses) > 0 {
			step.NewSqliteClasses = cloudflare.F(m.NewSQLiteClasses)
		}
		if len(m.NewClasses) > 0 {
			step.NewClasses = cloudflare.F(m.NewClasses)
		}
		if len(m.DeletedClasses) > 0 {
			step.DeletedClasses = cloudflare.F(m.DeletedClasses)
		}
		if len(m.RenamedClasses) > 0 {
			rs := make([]workers.MigrationStepRenamedClassParam, len(m.RenamedClasses))
			for i, r := range m.RenamedClasses {
				rs[i] = workers.MigrationStepRenamedClassParam{
					From: cloudflare.F(r.From),
					To:   cloudflare.F(r.To),
				}
			}
			step.RenamedClasses = cloudflare.F(rs)
		}
		if len(m.TransferredClasses) > 0 {
			ts := make([]workers.MigrationStepTransferredClassParam, len(m.TransferredClasses))
			for i, t := range m.TransferredClasses {
				ts[i] = workers.MigrationStepTransferredClassParam{
					From:       cloudflare.F(t.From),
					FromScript: cloudflare.F(t.FromScript),
					To:         cloudflare.F(t.To),
				}
			}
			step.TransferredClasses = cloudflare.F(ts)
		}
		steps = append(steps, step)
	}
	out := workers.ScriptUpdateParamsMetadataMigrationsWorkersMultipleStepMigrations{
		Steps: cloudflare.F(steps),
	}
	if len(ms) > 0 {
		out.NewTag = cloudflare.F(ms[len(ms)-1].Tag)
	}
	return out
}
