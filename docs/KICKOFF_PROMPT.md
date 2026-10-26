You are building `agent-dashboard`, a personal self-hosted web dashboard that manages git repos, coding agents and git operations from a laptop or phone. It runs on a small Linux VPS (2 vCPU, 2 GB RAM).

This repo contains the full spec. Before doing anything else, read these files completely, in this order:

1. `AGENTS.md` — how to build: stack, architecture rules (functional core / imperative shell), security rules, conventions, working agreement.
2. `PRD.md` — what to build and why.
3. `PLAN.md` — the order of work, split into phases with gates.

Your job right now is **Phase 0 (Foundation) only**. Do not start Phase 1.

Work like this:

1. Reply first with a summary of your understanding in at most 10 lines: what we are building, the Phase 0 deliverable, and the Gate 0 test. List any real blockers or ambiguities as short questions. Only ask what you cannot decide sensibly yourself; for everything else, pick the simplest option that satisfies the PRD and say what you chose.
2. Do the library spike from `PLAN.md` and write `docs/adr/0001-libraries.md`. Prefer the standard library and a few well-maintained packages. Show me the choices and wait for my OK before you write application code.
3. Then implement the Phase 0 tasks from `PLAN.md` in order, ticking each box in `PLAN.md` as you finish it. Keep the core pure and put effects in the shell, as described in `AGENTS.md`. Write tests as you go, including the bind-address refusal test and the secret-scrubbing test.
4. Security rules in `AGENTS.md` are non-negotiable. The server must never listen on a public address by default.
5. Run `make verify` until it is green.
6. Stop at Gate 0 and report: what you built, how to install and run it on the VPS (`scripts/install.sh`, systemd, Tailscale and swap-file notes), exactly what I should test from my phone, and anything you left out or are unsure about.

Rules:

- Never run destructive commands, never push, and never touch anything on this machine outside this repo without asking.
- Do not trust memory for CLI flags or library APIs; check docs or `--help`.
- If the PRD seems wrong or impractical, write an ADR in `docs/adr/` and tell me instead of silently changing course.
