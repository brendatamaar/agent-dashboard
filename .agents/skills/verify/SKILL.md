---
name: verify
description: Review, approve or revoke this repository's Model Functional Effect verify command — the command that runs at the end of turns that change functional-core files.
argument-hint: "[status|approve|revoke]"
---

# Verify command

The `verify` field in `.functional-effect.json` names a command (usually the core's property tests) that runs when functional-core files changed during a turn. A failing run sends the agent back to fix it, at most three times per turn.

The command comes from a file in the repository, so it never runs until the user approves it on this machine. The approval is bound to the exact command and the repository path, and is stored outside the repository; editing the command requires approving again.

## Steps

1. Show the current status:

   ```bash
   node .agents/scripts/fe-check.mjs approve --status --root .
   ```

   Exit code 2 means there is no config or no `verify` field: offer to add one (see below) and stop.

2. Argument `$ARGUMENTS`:
   - empty or `status`: report the status and explain what approving means. Stop.
   - `approve`: show the exact command and what it does (read the scripts or Makefile targets it calls if it is not obvious). Then run the approval below. The user confirms it in the permission prompt; never approve on the user's behalf by other means.

     ```bash
     node .agents/scripts/fe-check.mjs approve --root .
     ```

     Offer to run the verify command once now to confirm it passes.
   - `revoke`:

     ```bash
     node .agents/scripts/fe-check.mjs approve --revoke --root .
     ```

## Choosing a verify command

Keep it fast and focused on the core — it runs at the end of every turn that touches core files:

- Go: `go test ./internal/core/...`
- TypeScript: `npm test --silent` or `npx tsc -p . && node --test "dist/core/**/*.test.js"`

Use `{ "command": "...", "timeout": 120 }` to set a timeout in seconds (default 300, maximum 540).
