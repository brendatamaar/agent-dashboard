---
name: setup
description: Set up Model Functional Effect in the current repository — core/shell paths, the purity check, path-scoped rules, optional CI lint rules and permission policy.
---

# Set up Model Functional Effect

Configure this repository so the agent (layer 1: the code) and its own workflow (layer 2: plan → policy → interpret) follow the model. Ask before writing each file; never overwrite an existing file without showing the change.

## 1. Inspect

- Languages: `go.mod` → Go; `package.json` / `tsconfig.json` → TypeScript/JavaScript. A repo can have both. For TypeScript, note whether `package.json` depends on `effect` (use `../model/references/typescript-effect.md`) or not (use plain `../model/references/typescript.md`: zod, a `Deps` object, fast-check), and which of zod, fast-check or another schema/property library is already installed; suggest those over adding new ones.
- What the project does, to suggest a profile: money, inventory, queues, retries of non-idempotent calls, workers → `distributed`; HTTP services, jobs, tools calling databases and APIs → `service`; CLIs, libraries, parsers, UI reducers → `core`.
- Existing layout: look for directories that already hold pure domain logic (`internal/core`, `internal/domain`, `pkg/<x>/domain`, `src/core`, `src/domain`, `src/model`) and for shell code (`cmd`, `internal/http`, `internal/handler`, `internal/repository`, `src/api`, `src/server`, `src/shell`, `src/main.ts`).
- Existing `.functional-effect.json`, `.agents/rules/`, `AGENTS.md`, `.golangci.yml`, `eslint.config.*`.

## 2. Agree on paths

Propose `core` and `shell` globs that match the real layout (prefer existing domain folders over creating new ones). Present them in one question with the recommended option first. Also ask:

- `profile`: `core`, `service` or `distributed` (see [profiles](../model/references/profiles.md)). Recommend the lowest one that is honest about the failures the project cannot afford, with the reason, and say it can be raised later.
- `designDir`: where design documents live (default `docs/design`; use an existing docs folder if the repo has one).
- `mode`: `deny` (block edits that add effects and ask before new `fe:allow` exceptions; recommended) or `warn` (allow, but alert).
- Whether to enable the end-of-turn re-check of changed core files (`stopCheck`: `changed` or `off`; needs git).
- A `verify` command: the core's tests, run at the end of every turn that changes core files (e.g. `go test ./internal/core/...`). Recommend one; keep it fast.

## 3. Write `.functional-effect.json`

Start from `.agents/templates/functional-effect.json`, fill in the agreed values, drop empty `rules` sections. Field reference:

- `core` / `shell`: globs relative to the repo root.
- `profile`: `core` | `service` (default) | `distributed`. `designDir`: relative folder for design documents.
- `exclude`: extra globs to skip (tests, generated code and `.d.ts` are already skipped).
- `verify`: command string, or `{ "command": "...", "timeout": seconds }`.
- `rules.go|ts.forbidImports`: extra import prefixes to forbid in the core; `allowImports`: prefixes to always allow; `forbidCalls`: extra regexes.

The full schema is in `.agents/schema/functional-effect.schema.json`. Unknown keys and invalid values are reported as warnings at session start.

If a `verify` command was set, tell the user it does not run until they approve it, and offer the `verify` skill (`verify approve`). Do not approve it yourself.

## 4. Write the path-scoped rule

Copy `.agents/templates/rules/functional-effect.md` to `.agents/rules/functional-effect.md` (or merge into `AGENTS.md`), replacing the `paths:` list with the agreed `core` globs. It loads only when working on core files. If `core` changes later, update this file too.

## 5. Baseline scan

Run `node .agents/scripts/fe-check.mjs scan --root .` and report the result, including any reviewed `fe:allow` exceptions. Explain the ratchet: existing violations never block unrelated edits; only edits that add new effects are blocked, and only changes made during a turn are checked at its end (your own uncommitted edits are the baseline). Offer the `check` skill to work through existing ones later.

## 6. Optional: CI enforcement

Offer, per language detected, and show the snippet before merging:

- Go: merge `.agents/templates/golangci.depguard.yml` into `.golangci.yml` with the core path adjusted. It also enables `gochecksumtype`: add `//sumtype:decl` above the `State`, `Input` and `Command` interfaces so a type switch that misses a variant fails CI.
- TypeScript: add `.agents/templates/eslint.functional-core.mjs` to the repo and spread `functionalCore` into `eslint.config.*`, with `files` adjusted. It includes `switch-exhaustiveness-check`, which needs type information (`parserOptions.projectService`). Mention its typescript-eslint dependency.

## 7. Finish

List the files written, then suggest committing `.functional-effect.json`, `.agents/rules/functional-effect.md` and any settings so teammates get the same behavior. Mention the `design` skill as the way to start the next feature, and the `extract` skill for pulling decisions out of existing code that mixes logic with IO.
