# Copilot External Runtime

Use `copilot-external` when you want Gas Town to talk to a running Copilot headless server through the SDK instead of launching a local CLI process in tmux.

For the before/after design view, see
[`docs/design/copilot-external-architecture.md`](design/copilot-external-architecture.md).

## Start the server

```bash
copilot --headless --port 4321
```

## Example rig settings

Add an agent entry in `settings/config.json`:

```json
{
  "agents": {
    "copilot-external": {
      "provider": "copilot",
      "command": "copilot",
      "cli_url": "http://127.0.0.1:4321"
    }
  },
  "role_agents": {
    "witness": "copilot-external",
    "refinery": "copilot-external"
  }
}
```

## What works now

- `gt session smoke <rig> --cli-url http://127.0.0.1:4321`
- `witness` start/status/stop with `role_agents.witness = "copilot-external"`
- `refinery` start/status/stop with `role_agents.refinery = "copilot-external"`
- `witness attach` and `refinery attach` detect headless external sessions and print monitor/log guidance instead of trying to tmux-attach

## Live test target

Point `GT_TEST_COPILOT_CLI_URL` at a running headless Copilot server, then run:

```bash
make test-e2e-agent-external-copilot
```

That target runs:

- `TestExternalCopilotSessionSmoke`
- `TestWitnessLifecycleWithExternalCopilot`
- `TestRefineryLifecycleWithExternalCopilot`

The tests also accept `GT_EXTERNAL_COPILOT_CLI_URL` as a fallback env var.

## Notes

- Local CLI agents like `claude` or built-in `copilot` still work.
- External runtime roles use runtime session bindings in `.runtime/session-bindings/` as their source of truth.
- Owner-managed headless sessions write logs under `.runtime/copilot-owner/<session>/owner.log`.
- If the headless server is unavailable or unauthenticated, role startup will fail.
