# Code Quality Plan — Go

> **Enforcement model:** these rules apply to **new code and code we touch**. We do not stop work to retrofit the whole tree. Every PR leaves the area it touched cleaner than it found it (boy-scout rule). Track regressions in PR review.

## Size targets (soft caps)

| Unit | Target | Hard ceiling | Notes |
|---|---|---|---|
| File | ≤ 300 LOC | 500 LOC | Split by responsibility, not by line count. AWS SDK call sites can push toward the ceiling — that's OK if the file is one cohesive job. |
| Function | ≤ 20 LOC | 40 LOC | The 20-line target is aspirational. SDK input-struct literals don't count against the budget but should be extracted to a builder if reused. |
| Struct | ≤ 20 fields | 30 fields | If you cross 20, ask: is this two structs glued together? |
| Interface | ≤ 5 methods | 8 methods | Bigger interfaces = weaker abstractions. Prefer many small interfaces (`io.Reader`-style). |

When you touch a file >300 LOC: extract at least one cohesive unit out. Don't try to fix the whole file.

## Decoupling rules

1. **No SDK clients constructed in business logic.** `lambda.NewFromConfig(...)` belongs in a constructor or a `clients` struct on the provider — never inside a `Deploy*` or `Reconcile*` method. New code that does this gets bounced.
2. **Define small interfaces at the consumer side.** If `DeployCompute` needs only `GetFunction` and `UpdateFunctionCode`, declare a 2-method interface in the same file. Mock that. Don't depend on the full `*lambda.Client`.
3. **Orchestration layers don't import provider internals.** `deploy.go` orchestrates via the `Provider` interface only. If it needs an AWS-specific concept, the interface is wrong.
4. **One package = one responsibility.** When `serverless/` becomes painful (it already is), split into subpackages: `serverless/aws/lambda`, `serverless/aws/cloudfront`, etc. Don't pre-split — split when adding the next feature in that area.

## Error handling

1. **Errors are values — handle them, don't just return them.** Wrap with `fmt.Errorf("doing X for %s: %w", name, err)`. Future-you will thank you.
2. **Never match errors by `strings.Contains(err.Error(), ...)`** — use `errors.As` against typed SDK errors. The only exceptions: AWS errors that aren't exposed as types (document why with a comment).
3. **No silent `_ = doThing()`** — if you don't care about the error, write a one-line comment explaining why ("non-fatal: cleanup, see X").
4. **Distinguish fatal vs degraded.** A failed CloudWatch alarm is degraded (warn + continue). A failed Lambda update is fatal (return). Be explicit, not accidental.
5. **No `panic` outside `main` and tests.** Use `log.Fatal` in `main`, return errors elsewhere.

## Naming & structure

1. Package names: short, lowercase, no underscores, no `util`/`common`/`helpers`.
2. File names mirror the dominant type or responsibility (`lambda_url.go`, not `helpers.go`).
3. Constructors: `NewX` returns `*X` or `(X, error)`. No `MakeX`.
4. Public API needs a doc comment that starts with the identifier name (`golint` rule).
5. Acronyms stay uppercase: `URL`, `ID`, `ARN`, `OAC`, `IAM`. Not `Url`, `Id`.

## Concurrency

1. Channels for orchestration, mutexes for protecting state. Don't mix.
2. Always pass `context.Context` as the first parameter. Always honor cancellation.
3. No goroutines without a clear lifecycle — every goroutine needs a way to stop.

## Testing

1. New business logic ships with at least one test. Bug fixes ship with a regression test.
2. Tests depend on interfaces, not concrete SDK types. If you can't mock it, the design is wrong.
3. Prefer table-driven tests for anything with branches.
4. Integration tests live behind a build tag (`//go:build integration`).

## Comments & docs

1. Comments explain *why*, not *what*. The code shows what.
2. Every exported symbol has a doc comment. Unexported only when non-obvious.
3. `TODO` / `FIXME` must include a name or issue link. `// FIXME` alone is forbidden.
4. No commented-out code in commits. Delete it; git remembers.

## Forbidden patterns

- `interface{}` / `any` in public APIs (use generics or a real type).
- `init()` for anything other than registering with a registry.
- Global mutable state.
- `panic` for control flow.
- `reflect` outside encoding/decoding glue.
- Hand-rolled JSON parsing of secrets / config (use `encoding/json` + tagged structs).
- Hardcoded ARNs, account IDs, or region-specific resources without a config override.

## What "boy-scout" means in practice

When you open a file to fix bug X:

1. Fix bug X.
2. If the file is >300 LOC, extract one cohesive function or section into a new file.
3. If a function you touched is >40 LOC, split it.
4. If you spot a `strings.Contains(err.Error(), ...)` near your change, swap it for `errors.As`.
5. Stop. Don't refactor things you didn't touch — that goes in its own PR.

## Review checklist (paste into PR descriptions)

- [ ] No new file >300 LOC
- [ ] No new function >40 LOC
- [ ] No new SDK clients constructed inside business logic
- [ ] No new `strings.Contains(err.Error(), ...)`
- [ ] No new silent `_ =` on errors
- [ ] All exported symbols documented
- [ ] Boy-scout: at least one cleanup in a touched file >300 LOC
- [ ] Tests added or updated
