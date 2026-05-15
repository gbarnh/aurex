# aurex

A self-hosted, browser-based terminal workspace for Linux. Run it on your
laptop as a single Go binary; open the URL on your phone over Wi-Fi or
Tailscale and you have a real terminal — persistent tmux sessions, mobile
key toolbar, and a glowing "aura" ring with web push notifications when
an AI coding agent (Claude Code, Codex, Aider, …) is waiting for you.

No cloud. No relay. No runtime dependencies beyond tmux. One binary, MIT
license.

---

## Why

When an agent on your laptop is waiting on a y/n decision and you're three
rooms away, you want a glance at your phone to be enough. aurex is a tiny
control surface for that workflow:

- **Persistent sessions.** Closing the browser never kills a tmux session
  — the next connection re-attaches where you left off.
- **The aura.** Output is regex-matched (or explicitly poked via a hook
  endpoint). On a match, the session's card in the sidebar gets an
  animated cyan ring and your phone buzzes with a web push notification.
  Tap the notification → deep-link to that session.
- **Real cert, no warnings.** When Tailscale is detected, aurex pulls a
  Let's Encrypt cert via `tailscale cert` for the magic-DNS hostname,
  auto-renews daily. Otherwise it ships a self-signed cert with the right
  SANs for `localhost`+LAN IPs.
- **Cursor-protocol streaming.** Each session has a 2 MiB ring buffer with
  a monotonic byte cursor. Refresh, reconnect, switch devices — the new
  client passes its last cursor and the server fills in everything
  missed, no lost output.
- **Ghostty-web renderer.** The browser-side terminal is libghostty
  compiled to WASM, not xterm.js — proper VT100, grapheme handling,
  Devanagari/Arabic, the works.

---

## Quickstart

### Prereqs
- Linux (or macOS in WSL2-equivalent shape).
- `tmux` (`apt install tmux`, `dnf install tmux`, `brew install tmux`).
- Optional: Tailscale, for a real cert and a friction-free phone URL.

### Run from a release binary
```bash
curl -fsSL https://github.com/gbarnh/aurex/releases/latest/download/aurex-linux-amd64 -o aurex
chmod +x aurex
./aurex
```

You'll see:
```
aurex: open https://<your-host>.<your-tailnet>.ts.net:7681 on your phone — real cert, no warnings
```

(or the self-signed fallback URL if Tailscale isn't set up).

### Build from source
```bash
git clone git@github.com:gbarnh/aurex.git
cd aurex/client && npm ci && npm run build
cd .. && go build -o aurex .
./aurex
```

---

## Mobile

Open the URL on your phone (same Wi-Fi, or Tailscale). The first time:

1. (Android, self-signed only) Visit `chrome://flags/#unsafely-treat-insecure-origin-as-secure`,
   add your aurex origin, Relaunch. Or download the cert from the in-app
   push panel and install it as a CA. **Skip this entirely if you're on
   Tailscale** — the cert is real.
2. Tap **Notifications** in the sidebar → **Enable** → grant the system
   prompt → **Send test** to verify.
3. Create a session and start your agent. When it asks you something,
   your phone will buzz.

The toolbar at the bottom of the screen has CTRL / ESC / TAB / arrows —
the keys phone soft keyboards lack. CTRL is sticky-once: tap CTRL, then a
letter to send the control code.

---

## Agent hooks

Hooks are the precise way to trigger the aura without relying on output
regex. They're localhost-only by design.

```bash
curl -s -X POST http://localhost:7681/api/hook/aura \
  -H 'Content-Type: application/json' \
  -d '{"active": true, "reason": "Claude is waiting for input"}'
```

For Claude Code, add to `.claude/settings.json`:
```json
{
  "hooks": {
    "Stop": [{
      "matcher": "",
      "hooks": [{
        "type": "command",
        "command": "curl -s -X POST http://localhost:7681/api/hook/aura -H 'Content-Type: application/json' -d '{\"active\":true,\"reason\":\"Claude is waiting for input\"}'"
      }]
    }]
  }
}
```

---

## Config

First run writes `aurex.config.json` in the working directory. Defaults:

```json
{
  "port": 7681,
  "auth": false,
  "username": "aurex",
  "password": "changeme",
  "defaultShell": "bash",
  "tmuxPrefix": "aurex",
  "tls": true,
  "tlsCertFile": "aurex.cert.pem",
  "tlsKeyFile": "aurex.key.pem",
  "tailscale": "auto",
  "tailscaleCertFile": "aurex.ts.cert.pem",
  "tailscaleKeyFile": "aurex.ts.key.pem",
  "httpRedirectPort": 7680,
  "pushSubscriptionsFile": "aurex.subscriptions.json"
}
```

VAPID push keys are generated and written here on first run. **Don't
regenerate** — that invalidates every push subscription.

The Tailscale path needs two one-time setup steps:
```bash
sudo tailscale set --operator=$USER     # let aurex fetch certs unprivileged
# then enable HTTPS in your tailnet admin console at:
#   https://login.tailscale.com/admin/dns
```

---

## Status

This is **v0.1.0** — early, single-author, self-hosted. It works for my
"laptop in the office, phone on the couch" workflow and is intentionally
small. The architecture is modeled after the relevant pieces of
[opencode](https://github.com/sst/opencode)'s session/PTY/cursor design —
that's where most of the polish ideas came from.

A SaaS version with a hosted relay is on the medium-term roadmap; this
OSS binary will always be free and MIT.

## License

MIT. See [LICENSE](./LICENSE).
