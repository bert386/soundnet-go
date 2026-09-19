# AI Agent Instructions for SoundNet

These instructions apply to all AI coding agents working on this repository,
regardless of tool (Claude Code, Codex, Cursor, Gemini, Windsurf, Copilot, etc.).

## SoundNet fork - architectural rules

SoundNet is a private, non-commercial fork of BirdNET-Go, extended from a bird-focused
soundscape analyser into a general acoustic event detector. Read `doc/soundnet/SCOPE.md`
for the full brief and `doc/soundnet/DECISIONS.md` for decisions already made.

These rules override nothing below; they are additional and they are not negotiable.

### Keep upstream mergeable

This fork tracks upstream. Every avoidable edit to an upstream file costs a merge conflict
later, forever.

- **All new code goes in new `internal/*` packages.** Do not grow upstream packages.
- **Touch upstream files with thin hooks only** - ideally one clearly-marked integration
  point per pipeline stage, calling into a new package.
- Before merging upstream, run `scripts/rewrite-module-path.sh` on the upstream branch so
  its import paths match the fork's. That converts ~1,190 conflicting files into a clean merge.
- The module path is `github.com/bert386/soundnet-go`. External dependency
  `github.com/tphakala/go-tflite` is NOT part of the rename - leave it alone.

### The three layers are separate, and must stay separate

| Layer | Package | Emits |
|---|---|---|
| Class | models / gallery | a coarse label |
| Properties | `internal/diagnostics` | measurements - near/far, speed, count, duration |
| Identity | `internal/enrichment` | an externally-resolved identity |

Do not let these bleed into one another. Diagnostics emit **measurements, not labels**.
Enrichment resolves identity from an authoritative external source or **returns null** -
never a guess. For siren, gunshot and vehicle there is no public authority: returning
nothing is the correct behaviour, and fabricating an identity is a defect.

Where a discrimination is genuinely unreliable from a single microphone - gunshot versus
vehicle backfire is the known case - expose the features and a **low-confidence flag**.
Do not assert.

### Cost and configuration

- **Perf budget is a Raspberry Pi 4.** Diagnostics must run under 100 ms per clip. Every
  stage must be skippable per class. Benchmark in CI.
- **All new behaviour sits behind `config.yaml` keys, defaulting to off where it adds cost.**
  Validate configuration on load.
- **Privacy by design, matching upstream.** No external call without explicit opt-in.
  Provider credentials are read from an operator-supplied path or env var - never committed,
  never written into a versioned `config.yaml`, never logged or included in support dumps.
- **No network in tests.** Provider tests run against captured fixture snapshots.

### Attribution is a licence obligation

`LICENSE`, `NOTICE` and `AUTHORS` credit upstream and must not be rewritten to remove that
credit. CC BY-NC-SA 4.0 requires it. Automated rewrites must exclude these files.

## PR Scope Rule

Each pull request must contain exactly ONE of:
- One feature
- One bug fix
- One refactor

PRs that batch multiple features or multiple fixes WILL NOT be merged.
This is non-negotiable. If your task involves multiple independent changes,
split them into separate branches and separate PRs.

If you are uncertain whether changes constitute one concern or multiple,
ask the user before proceeding.

Why: batched PRs cannot be properly reviewed, cannot be safely reverted
if one change causes a regression, and create merge conflicts for other
contributors.

## Mandatory: Pre-Push Quality Gate

Before pushing code or creating a pull request, you MUST execute the
preflight quality gate defined in `.agents/skills/preflight/SKILL.md`.

Read that file and follow its complete process (all phases).
Do not skip this step. Do not push without running it first.

If your platform supports native skill invocation (e.g., Claude Code's
`/preflight` or Codex's skill system), use that. Otherwise, read the
SKILL.md file directly and execute the review process.

## PR Creation Rules

When creating a pull request, you MUST:

1. Verify this PR addresses exactly ONE feature, fix, or refactor
2. Include a "Preflight Status" section in the PR description showing
   what was found and fixed during preflight
3. Verify all linters pass (`golangci-lint run -v`, `npm run check:all`)
4. Verify all tests pass (`go test -race ./...`, `npm test`)
5. Confirm the diff contains ONLY changes relevant to the stated goal
6. Confirm scope is complete (no TODO/FIXME for core functionality)
7. Confirm no secrets, credentials, or PII in the diff
8. Document any breaking changes to API, config, or behavior

You MUST actually execute verification commands (linters, tests) and
observe passing output before claiming they pass. Do not check boxes
based on assumption or prior knowledge.

PRs missing the preflight certification will require multiple review
rounds. The gate catches the same issues reviewers find; running it
locally saves a day of back-and-forth.

## Interpreting CI Failures

The `golangci-test` workflow runs Go tests through `gotestsum` with one
automatic rerun of any failed test, then publishes a consolidated result in the
`test-report` job. Before assuming a red run means your code is broken:

1. Read the `test-report` job summary. It states one verdict:
   - `REGRESSION` - real failures that persisted after a rerun. Fix these.
   - `PASS (with flakes)` - tests that failed once then passed on rerun. These
     are flaky/infra (a reaped container, a registry blip), NOT a code
     regression. Do not "fix" them; re-run or report instead.
   - `PASS` - all green.
2. For machine-readable detail, download the `ci-failures` artifact:
   - `ci-failures.json` - array of real regressions (`{pkg, test, output}`).
   - `ci-flaky.json` - tests that passed on rerun (informational).
   Prefer reading these small files over scrolling the raw multi-thousand-line
   logs.
3. If a testcontainer job failed, the job summary includes a "Testcontainer
   diagnostics" block (docker state, memory, OOM kills) to distinguish an
   infra flake from a logic bug.

Do not spend time debugging a failure classified as flaky/infra. If a test is
persistently flaky, raise it rather than patching around it.

## Project Context

- Tech stack: Go 1.24+, Svelte 5, TypeScript, Tailwind v4.1
- Build system: Task (taskfile.dev)
- Linting: golangci-lint (Go), npm run check:all (Frontend)
- Testing: go test -race (Go), npm test (Frontend)
- All PRs receive automated CodeRabbit reviews
- API v1 is frozen; all new endpoints go in `internal/api/v2/`

For detailed guidelines, see `CLAUDE.md` and the `CLAUDE.md` files in
subdirectories (`internal/`, `frontend/`, `internal/api/v2/`).
