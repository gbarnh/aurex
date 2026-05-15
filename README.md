# aurex

> Remote, OS-agnostic orchestration for your AI coding agents. Run Claude
> Code (or Codex, or Aider) on your laptop; watch and steer it from any
> device with a browser.

Single Go binary. Self-hosted. MIT.

---

## What is this

[cmux](https://github.com/manaflow-ai/cmux) is a great answer to a real
problem: when five Claude Code agents are running in five panes, you
can't tell which one is asking you something. cmux solves that with a
native macOS terminal that adds a notification ring + a sidebar with
git/PR/cwd context. Several people are working on Linux ports.

**Aurex skips the port-per-OS problem entirely.** The server is a single
Go binary that runs on anything with tmux + a Unix PTY (Linux, macOS,
BSD, WSL). The client is the *browser* — which means the same polished
experience on a Mac, a Linux box, a Windows desktop, an iPad, an Android
phone, or a Chromebook. Nothing OS-specific to build.

Same idea cmux is famous for — animated ring on the active-prompt
session, sidebar with branch/cwd, ghostty-based rendering — shaped as a
self-hosted web app you can reach over Tailscale from anywhere. The
agents live on your laptop. The control surface comes with you.

The workflow this is built for:

- **Laptop**: agents run, do their thing for minutes-to-hours.
- **You**: anywhere — couch, errands, a different machine, on cellular.
- **Phone**: buzzes via web push when an agent hits a y/n prompt.
  Tap → land in that session's terminal. Type `y`, walk away.

---

## Why it exists

A locally-run agent gives you privacy and capability. Aurex adds the
"I can step away from my desk" affordance that hosted services give you
for free, without giving up either of the first two.

---

## Architecture

- **One PTY per session, owned by the server.** WebSockets are
  subscribers, not attachers — disconnect, refresh, switch devices and
  the tmux session stays exactly where it was.
- **Cursor-protocol streaming.** Each session has a 2 MiB ring buffer
  with a monotonic byte cursor (modeled on
  [opencode](https://github.com/sst/opencode)'s design). Clients pass
  their last cursor on reconnect and get only what they missed.
- **Ghostty-web renderer.** libghostty compiled to WASM — real VT100,
  grapheme handling, the works.
- **Tailscale-issued real cert.** When Tailscale is present aurex pulls
  a Let's Encrypt cert via `tailscale cert` for the magic-DNS hostname
  and auto-renews.

---

## Two modes

Aurex runs in two modes depending on whether you want web push
notifications to your phone.

| | **LAN-only** | **Full (with Tailscale)** |
|---|---|---|
| Terminal access | ✓ over `http://host:7681` | ✓ over `https://host.tailnet.ts.net:7681` |
| Reach from outside LAN | ✗ | ✓ |
| Web push to phone | ✗ (browsers require HTTPS) | ✓ |
| Setup | none beyond `tmux` | install Tailscale, one config knob |
| Cert | none | real Let's Encrypt, auto-renewing |

If you're happy staying on your home network and don't need a buzz on
your phone when an agent asks something, skip Tailscale entirely. The
terminal, sidebar, sessions, hooks, and everything else work over plain
HTTP. Push notifications are the only feature gated on HTTPS, and the
only reason aurex involves Tailscale at all.

---

## Quickstart

### Prereqs (both modes)
- Linux or macOS server (something with tmux + Unix PTY).
- `tmux` (`apt install tmux`, `dnf install tmux`, `brew install tmux`).

### LAN-only (no Tailscale, no push)

Set `"tailscale": "off"` in `aurex.config.json` once it's been generated
(or do it on first run by creating the file beforehand). Then:

```bash
curl -fsSL https://github.com/gbarnh/aurex/releases/latest/download/aurex-linux-amd64 -o aurex
chmod +x aurex
./aurex
```

Output:
```
aurex: listening on http://0.0.0.0:7681 — install Tailscale and set HTTPS in the admin console for push notifications
```

Open `http://<server-LAN-IP>:7681` from any device on the same network.

### Full mode (Tailscale + push)

One-time Tailscale setup:
```bash
sudo tailscale set --operator=$USER     # let aurex fetch certs unprivileged
```
Enable HTTPS in the admin console:
<https://login.tailscale.com/admin/dns> → "Enable HTTPS…".

Then install Tailscale on every device you want to reach the server
from (phone included) and run aurex:

```bash
curl -fsSL https://github.com/gbarnh/aurex/releases/latest/download/aurex-linux-amd64 -o aurex
chmod +x aurex
./aurex
```

Output:
```
aurex: using Tailscale cert for laptop.your-tailnet.ts.net (auto-renew on restart)
aurex: open https://laptop.your-tailnet.ts.net:7681 on your phone — real cert, no warnings
```

### Build from source
```bash
git clone git@github.com:gbarnh/aurex.git
cd aurex/client && npm ci && npm run build
cd .. && go build -o aurex .
./aurex
```

---

## Agent hooks

To trigger the aura and push without relying on output regex, agents can
poke a localhost endpoint:

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

## Mobile

Open the Tailscale URL on your phone. Tap **Notifications** in the
sidebar → **Enable** → grant the system prompt → **Send test**.

The toolbar below the terminal has CTRL / ESC / TAB / arrows — the keys
phone soft keyboards lack. CTRL is sticky-once: tap CTRL, then a letter
to send the control code.

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
  "httpRedirectPort": 7680,
  "tailscale": "auto",
  "tailscaleCertFile": "aurex.ts.cert.pem",
  "tailscaleKeyFile":  "aurex.ts.key.pem",
  "pushSubscriptionsFile": "aurex.subscriptions.json"
}
```

VAPID push keys are generated and persisted on first run. Don't
regenerate — that invalidates every push subscription.

`tailscale` accepts `"auto"` (use Tailscale if available, fall back to
plain HTTP), `"on"` (require Tailscale, refuse to start without it), or
`"off"` (skip TLS, HTTP only — push won't work).

---

## Status

**v0.1.0.** A SaaS variant with a hosted relay is on the roadmap; this
OSS binary will always be free and MIT.

## License

MIT. See [LICENSE](./LICENSE).
