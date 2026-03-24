# Copilot Hook Payload Probe

This note describes a safe, reproducible way to inspect the JSON payload that
GitHub Copilot CLI sends to Gas Town hook commands.

## Goal

We want to know whether Copilot exposes enough structured post-tool metadata to
enforce side-effect postconditions such as:

- after `gt mail send`, verify the target inbox shows the message
- after file creation, verify the file exists

At the moment, Gastown only relies on a known-good `preToolUse` payload shape in
the built-in Copilot hook template:

- `toolName`
- `toolArgs.command` for `bash`

We do **not** yet have a trusted contract for a `postToolUse` payload with
fields like exit code, stdout, stderr, or final tool result.

## Recommended Probe Strategy

Use an isolated workspace and a temporary Copilot hook file that logs raw hook
stdin JSON to disk.

Do not add speculative verification logic to the production hook template until
the payload shape is proven stable.

## Minimal Probe Hook

Create a temporary `.github/hooks/gastown.json` like this in a scratch repo:

```json
{
  "version": 1,
  "hooks": {
    "preToolUse": [
      {
        "type": "command",
        "bash": "mkdir -p .copilot-hook-probe && cat > .copilot-hook-probe/pre-$(date +%s%N).json",
        "timeoutSec": 10
      }
    ],
    "postToolUse": [
      {
        "type": "command",
        "bash": "mkdir -p .copilot-hook-probe && cat > .copilot-hook-probe/post-$(date +%s%N).json",
        "timeoutSec": 10
      }
    ]
  }
}
```

Then run Copilot in that repo and trigger a few tool calls:

- a successful `bash` command
- a failing `bash` command
- a non-bash tool call if available

Inspect the captured JSON files in `.copilot-hook-probe/`.

## What To Look For

For `preToolUse`:

- `toolName`
- `toolArgs.command`
- any stable request/session identifiers

For `postToolUse`:

- whether the event exists at all
- whether it includes the same `toolName`
- whether it includes `toolArgs.command`
- whether it includes an exit status
- whether it includes stdout/stderr
- whether it includes a structured success/failure field

## Acceptance Criteria For Production Use

Only promote this into the production Copilot hook template if all of these are
true:

1. `postToolUse` fires reliably for the tool types we care about
2. the payload contains stable fields for command identity and result status
3. those fields are present across success and failure cases
4. a missing or malformed payload can fail open without breaking normal work

## Relation To Existing Gas Town Tooling

For raw SDK validation, use:

- `tmp_copilot_repro.go`

That harness is useful for proving Copilot session/tool behavior, but it does
not itself expose hook payload contents. Hook payload probing should happen in a
scratch workspace with the temporary JSON logger above.

## Current Status

As of the current Copilot runtime hardening work:

- Gastown has a stable `preToolUse` guard for PR workflow blocking
- Gastown does **not** yet assume a stable `postToolUse` payload contract
- manual verification guidance remains the correct fallback for side effects

## Next Step

After collecting real probe samples, add one of:

- a small fixture doc with redacted sample payloads, or
- a targeted production `postToolUse` verifier if the payload is strong enough
