# Gas Town Mail Architecture

Design and runtime reference for the Gastown mail system.

## Scope

This document covers how mail is stored, routed, listed, acknowledged, and
notified at runtime.

It complements:

- `docs/design/mail-protocol.md` for message shapes and protocol conventions
- `docs/design/architecture.md` for town/rig storage layout

## Design Goals

- Durable inter-agent messaging that survives session restarts
- One authoritative mailbox store for the whole town
- Address normalization that works across crew, polecats, and town-level roles
- Fast inbox reads with explicit delivery tracking
- Best-effort wakeup notifications without making notification success part of
  message durability

## Core Model

Mail is stored as Beads records with `gt:message` labels in the town-level
`.beads` database.

- Town mail store: `<town>/.beads`
- Project issue stores: `<rig>/mayor/rig/.beads` and redirected clones
- Rule: all mail uses town beads, even when the sender or recipient lives in a
  rig workspace

This split is enforced by `findMailWorkDir()` in `internal/cmd/mail_identity.go`
and `Router.resolveBeadsDir()` in `internal/mail/router.go`.

## Main Components

### CLI layer

`internal/cmd/mail_*.go` handles:

- sender detection
- town-root resolution
- command parsing and output formatting

Important entry points:

- `runMailSend()` in `internal/cmd/mail_send.go`
- `runMailInbox()` / `runMailRead()` / `runMailSearch()`
- `runMailCheck()` in `internal/cmd/mail_check.go`

### Routing layer

`internal/mail/router.go` handles:

- address validation and normalization
- list, queue, channel, and announce expansion
- durable writes via `bd create`
- async notifications via tmux nudges or queued nudges

Main methods:

- `Router.Send()`
- `Router.GetMailbox()`
- `Router.notifyRecipient()`

### Mailbox layer

`internal/mail/mailbox.go` handles:

- inbox listing
- unread filtering
- archived search
- delivery acknowledgement
- wisp and issue queries against beads

Main methods:

- `Mailbox.List()`
- `Mailbox.ListUnread()`
- `Mailbox.Search()`
- `Mailbox.AcknowledgeDeliveries()`

### Delivery tracking layer

`internal/mail/delivery.go` implements a two-phase delivery model:

- send phase: `delivery:pending`
- ack phase: `delivery:acked`, `delivery-acked-by:<identity>`,
  `delivery-acked-at:<timestamp>`

This distinguishes durable write from recipient observation.

## Address And Identity Model

Addresses are user-facing. Identities are the canonical Beads assignees.

Examples:

| Address | Canonical identity |
|---|---|
| `mayor/` | `mayor/` |
| `deacon/` | `deacon/` |
| `gastown/crew/max` | `gastown/max` |
| `gastown/polecats/alfa` | `gastown/alfa` |
| `gastown/refinery` | `gastown/refinery` |

Normalization lives in `AddressToIdentity()` in `internal/mail/types.go`.

Session wakeups use `AddressToSessionIDs()` in `internal/mail/router.go`, which
maps a canonical mail address to one or more tmux session candidates.

## Send Path

### 1. Sender and town resolution

`gt mail send` does two important things before it writes anything:

- detects the sender with `detectSender()`
- resolves the town root with `findMailWorkDir()`

For agent sessions, `GT_ROLE`, `GT_RIG`, `GT_CREW`, and `GT_POLECAT` are the
authoritative sender inputs.

### 2. Message construction

`runMailSend()` builds a `mail.Message` with:

- sender
- recipient
- subject/body
- thread id
- message type
- priority
- optional CC

### 3. Durable write

`Router.Send()` chooses one of several paths:

- `sendToSingle()`
- `sendToList()`
- `sendToQueue()`
- `sendToAnnounce()`
- `sendToChannel()`

For direct mail, `sendToSingle()` does the durable write by calling `bd create`
through `runBdCommand()` in `internal/mail/bd.go`.

Write-time labels include:

- `gt:message`
- `from:<sender>`
- `msg-type:<type>`
- `delivery:pending`
- thread/reply/cc labels when present

If `Router.Send()` returns success, the message has already been durably written
to town beads.

### 4. Notification

Notification happens after the durable write and is intentionally asynchronous.

`notifyRecipient()` tries, in order:

- immediate tmux nudge when the recipient is idle
- queued nudge when the recipient is busy
- ACP/queue fallback when no tmux session exists

Notification failure does not roll back the durable mail write.

## Read Path

### Inbox

`gt mail inbox` uses `Router.GetMailbox()` and `Mailbox.List()`.

`Mailbox.List()` reads from the town mail store and merges:

- direct assignee matches
- CC matches
- wisp-backed messages from the wisps table

Results are sorted by priority first, then timestamp.

### Unread

`Mailbox.ListUnread()` filters `Mailbox.List()` on the message's read state.

### Search

`Mailbox.Search()` searches both inbox and archive content.

Operational gotcha: `gt mail search` searches the current sender's mailbox by
default. Use `--identity <address>` to search a different inbox. Unlike
`gt mail inbox`, it does not accept a positional address argument.

### Read and delivery ack

`gt mail check --inject` and mailbox reads can acknowledge delivery through
`Mailbox.AcknowledgeDeliveries()`.

Ack is separate from marking a message read:

- delivery ack = recipient observed the mail
- read label = user/agent marked it read in the inbox UI

## Notification Path

Notification is intentionally separate from mail durability.

`notifyRecipient()` uses tmux liveness and idle detection:

- `WaitForIdle()` determines whether immediate delivery is safe
- `NudgeSession()` injects a notification into an idle session
- `nudge.Enqueue()` queues a cooperative reminder for a busy session

Reply reminders are added by `enqueueReplyReminder()` so agents are nudged to
respond with `gt mail send` instead of chat text.

## Delivery Semantics

The mail system is not eventually consistent at the write layer.

- durable write: synchronous
- inbox visibility: immediate after successful `bd create`
- recipient notification: async, best-effort
- delivery ack: async, recipient-driven

That means an apparent "mail timing gap" is usually not a mail-store delay.

## Investigation Finding: The Recent "Timing Gap"

During Copilot worker validation, the worker UI claimed it had sent completion
mail before the mayor inbox showed the new message.

Investigation result:

- the mail layer itself is synchronous
- successful sends appear immediately in mayor inbox and `.events.jsonl`
- direct probes from `gastown/max` to `mayor/` confirmed this behavior
- the apparent gap came from the agent/runtime layer: a Copilot worker can
  claim it executed `gt mail send` without a matching durable side effect

In other words, the observed gap was between agent narration and durable mail
commit, not between commit and inbox visibility.

## Debugging Checklist

Use these in order:

1. Check the target inbox directly:
   - `gt mail inbox mayor/`
   - `gt mail inbox gastown/crew/max`
2. Read the specific message by id when available:
   - `gt mail read <id>`
3. Search from the correct identity context:
   - `gt mail search "subject" --subject --identity mayor/`
   - `GT_ROLE=mayor gt mail search "subject" --subject`
4. Check the feed log:
   - `.events.jsonl`
5. Check notification evidence:
   - `.runtime/nudge_queue/<session>/`

If the inbox and `.events.jsonl` do not show a send, the message was not
durably committed even if an agent claimed success in its UI.

## Key Source Files

- `internal/cmd/mail_send.go`
- `internal/cmd/mail_identity.go`
- `internal/cmd/mail_check.go`
- `internal/mail/router.go`
- `internal/mail/mailbox.go`
- `internal/mail/delivery.go`
- `internal/mail/types.go`
- `internal/mail/bd.go`

## See Also

- `docs/design/mail-protocol.md`
- `docs/design/architecture.md`
- `docs/design/dolt-storage.md`
