---
title: The send gate
description: Every write tool is held until you approve it. The check lives in the server, not the prompt.
---

Reading is free. Every tool that touches someone else's phone is held until you
say yes.

The important part is *where* the check lives: **in the binary, not in the system
prompt.** The model is never asked for permission and is never given a way to
grant it. Jailbreaks work on instructions. This is not an instruction.

## The only route

```
the model  →  the gate (held)  →  you  →  delivered
```

There is no second path. Twelve of the twenty-eight tools are gated, and there is
no unguarded cousin of `send_message`, no fast path and no flag that turns the
gate off.

## What the gate guarantees

| | |
|---|---|
| **Per message** | Approval covers one message to one chat. There is no "allow for the next hour" and no remembered consent. |
| **Every write** | All twelve write tools, without exception. |
| **Rate limited** | Even approved sends are throttled, so a loop cannot become a hundred messages while you are away from the keyboard. |
| **Allowed folders only** | Media can be sent only from directories you named. The model cannot reach into your home folder and pick a file. |
| **Logged** | Every gated call is recorded with what it wanted to do, whether you allowed it, and when. Including the ones you refused. |

## What it does not protect you from

The gate removes one half of the ban risk: bulk sends, and a model going off on
its own. It cannot touch the other half, which is that this client is
identifiable as a third-party client at all. Read [Ban risk](/docs/ban-risk).
