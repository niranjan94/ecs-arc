# AGENTS.md

This file provides guidance to Autonomous Coding Agents such as Claude Code, Codex, OpenCode when working with code in this repository.

## Commands

- Build: `go build ./cmd/ecs-arc`
- CLI has a single cobra subcommand: `controller` (long-running autoscaler). No `generate-template`, `delete-scalesets`, etc. -- add new subcommands under `cmd/ecs-arc/` if needed.
- Test (matches CI): `go test ./... -v -race`
- Single package: `go test -v -race ./internal/reconciler`
- Single test: `go test -v -race ./internal/reconciler -run TestReconciler_StartupOrphanCleanup`
- Vet: `go vet ./...`
- Lint: `golangci-lint run` (CI uses `latest`; no repo-level config, pure defaults)
- Run locally: see README "Running Locally" — needs GitHub App creds, `ECS_CLUSTER`, and `SSM_PARAMETER_NAME` pointing at a TOML config

Module targets `go 1.26.1` (see `go.mod`); use the same toolchain as CI via `go-version-file: go.mod`.

## Specs and plans

- Design specs live in `docs/specs/`.
- Implementation plans live in `docs/plans/`.
- Both directories are gitignored (see `.gitignore`). Treat them as scratch space for the current session; they are not part of version control and will not persist across machines or for collaborators.

## Commit convention

Conventional Commits, scoped by area (`feat(deploy):`, `fix(reconciler):`, `refactor:`, `ci:`, `test:`, etc.). Keep the existing style when adding commits.

## Architecture

`ecs-arc` is a single Go binary that autoscales GitHub Actions self-hosted runners as ECS tasks. It replaces the Kubernetes plane of `actions-runner-controller` with AWS ECS, using the upstream `github.com/actions/scaleset` library to speak the internal Actions service scale-set API.

The runtime is a pipeline of three cooperating layers:

1. **Reconciler** (`internal/reconciler`) — long-polls SSM for a TOML config, expands templates (`internal/tomlcfg`), diffs against observed state, and registers / deregisters ECS task definitions. It emits `ReconcileEvent{Create|Update|Remove}` on a channel. On startup it also performs orphan cleanup: task definitions tagged as managed but no longer in TOML get deregistered.
2. **Controller** (`internal/controller`) — consumes reconciler events and manages one goroutine per scale set. `EventCreate` spawns a scale set goroutine; `EventRemove` cancels its context; `EventUpdate` logs a warning (config changes require restart). Shutdown cancels all per-scale-set contexts and waits.
3. **Per-scale-set goroutine** — creates (or updates) the scale set registration on GitHub, opens a `MessageSessionClient` (retries on 409 `SessionConflictException`), and runs the `listener` from the upstream library. `internal/scaler` handles the `HandleDesiredRunnerCount` / `HandleJobStarted` / `HandleJobCompleted` callbacks, calling ECS `RunTask` with a JIT config injected via `ACTIONS_RUNNER_INPUT_JITCONFIG`. `internal/runner` wraps the ECS client and runs a reaper goroutine that stops tasks stuck in PENDING or exceeding `max_runtime`.

### Non-obvious behaviors to preserve

- **Scale set registrations are intentionally NOT deleted on shutdown** (`internal/controller/controller.go` around the "Scale set registrations are deliberately NOT deleted" comment). During ECS deployments the old task stops before the new one starts; deleting the registration would create a gap where GitHub sees no scale set and queued jobs fail. The new instance re-enters via `CreateRunnerScaleSet` → "already exists" → `UpdateRunnerScaleSet`. Do not "fix" this by adding teardown.
- **TOML expansion is central.** `[[template]]` blocks enumerate `sizes × features` into concrete runner variants; `[[runner]]` blocks add one-offs. All downstream code sees a flat `map[family]ResolvedRunnerConfig` — templates do not exist at runtime.
- **Config changes for a running scale set require a restart.** The controller deliberately does not hot-reload `EventUpdate`; only `EventCreate`/`EventRemove` change live state.
- **`SCALESET_NAME_PREFIX`** shifts the scale set name (and therefore the GitHub runner label) to `{prefix}-{family}` but leaves the ECS task definition family name alone. Workflows must target the prefixed label.
- **Per-scale-set configuration lives entirely in TOML**, not on the ECS task definition. `internal/taskdef.ScaleSetConfig` is populated from the `ResolvedRunnerConfig` the reconciler produces. There is no task-definition-tag parser; earlier designs had one, but it was removed when TOML-driven config landed. Don't add one back without an explicit design decision.

### Deployment

`deploy/template.yaml` is a **static** CloudFormation template, copied as-is by the release workflow (see commits `c9c746f` and `8b194f5`). There is no template generator; if you need one again, restore it as a new cobra subcommand instead of rewriting the static file at deploy time.

## Test coverage notes

- `internal/reconciler/reconciler_test.go` contains the end-to-end integration test `TestIntegration_FullPipeline` (fake SSM + ECS clients exercising the full reconcile → register → event flow) plus focused cases like `TestReconciler_StartupOrphanCleanup`. Use these as templates for new reconciler behavior.
- `internal/tomlcfg/tomlcfg_test.go` is by far the largest test file and pins template-expansion semantics; changes to expansion rules need updates here.
