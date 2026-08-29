---
title: Run as a service
description: Keep the http server alive across logouts and reboots with launchd, a systemd user unit, or a Task Scheduler logon task.
---

Only relevant if you chose the [http transport](/docs/transports).

```sh
whatsapp-connect-mcp service install
```

That installs the right thing for your platform: **launchd** on macOS, a
**systemd user unit** on Linux, a **Task Scheduler logon task** on Windows.

```sh
whatsapp-connect-mcp service restart    # after an upgrade
whatsapp-connect-mcp service uninstall
```

## Platform notes

**Linux, headless.** Add lingering so the service outlives your login session:

```sh
loginctl enable-linger "$USER"
```

**Windows.** The service runs as a minimized console window that appears at user
logon, not at boot. Closing that window stops the server. Creating the task may
need an elevated (Administrator) terminal.

**No crash restart.** There is deliberately no automatic restart. `serve` in an
unpaired state waits idle rather than exiting, so the common failure mode never
exits anyway — a restart loop would just hide it from you.
