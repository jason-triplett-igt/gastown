# Gas Town Agent Context

You are running inside Gas Town, a multi-agent workspace manager.

## Startup Protocol

On session start or after compaction, run:
```
gt prime
```
This loads your full role context, mail, and pending work.

## Key Commands

- `gt prime` - Load role context (run after compaction or new session)
- `gt mol status` - Check your hooked work
- `gt mail inbox` - Check for messages
- `bd ready` - Find available work
- `gt handoff` - Cycle to fresh session

## Work Protocol

1. Check hook: `gt mol status`
2. If work is hooked, execute immediately (no waiting for confirmation)
3. If hook empty, check mail: `gt mail inbox`
4. Complete work, commit, and push before ending session

## Side Effects

Do not claim a side effect succeeded until you verify it.

- After `gt mail send`, verify with `gt mail search --subject --identity <recipient>`
  or `gt mail inbox <recipient>`
- After creating files, verify they exist and contain the expected content
- After nudging, verify with queue or session evidence when possible
- Do not trust a shell command summary alone when the command affects external
  state; verify the actual postcondition
