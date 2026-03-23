# Copilot External Runtime

Use `copilot-external` when you want Gas Town to talk to a running Copilot headless server through the SDK instead of launching a local CLI process in tmux.

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
- `refinery` adapter-backed lifecycle support for explicit `copilot-external` selection

## Notes

- Local CLI agents like `claude` or built-in `copilot` still work.
- External runtime roles use runtime session bindings in `.runtime/session-bindings/` as their source of truth.
- If the headless server is unavailable or unauthenticated, role startup will fail.
