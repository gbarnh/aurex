# aurex

> Remote, OS-agnostic orchestration for your AI coding agents. Run Claude
> Code (or Codex, or Aider) on your laptop; watch and steer it from any
> device with a browser.

Single Go binary. Self-hosted. MIT.

---

## What is this

[cmux](https://github.com/manaflow-ai/cmux) is a great answer to a real
problem: when you've got five Claude Code agents running in five panes,
you can't tell which one is asking you something. cmux solves that with
a native macOS terminal that adds a notification ring + a sidebar with
git/PR/cwd context. Several people are working on Linux ports.

**Aurex skips the port-per-OS problem entirely.** The server is a single
Go binary that runs on anything with tmux + a Unix PTY (Linux, macOS,
BSD, WSL). The client is the *browser* — which means the same polished
experience on a Mac, a Linux box, a Windows desktop, an iPad, an
Android phone, or your wife's Chromebook. There's no "wait for the
Linux build" because there's nothing OS-specific to build.

Same idea cmux is famous for — animated ring on the active-prompt
session, sidebar with branch/cwd, ghostty-based rendering — but shaped
as a self-hosted web app you can reach over Tailscale from anywhere.
The agents still live on your laptop. The control surface comes with
you.

The workflow this is built for:

- **Laptop**: agents run, do their thing for minutes-to-hours.
- **You**: anywhere — couch, errands, a different machine, on cellular.
- **Phone**: buzzes via web push when an agent hits a y/n prompt.
  Tap → land in that session's terminal. Type `y`, walk away.

---

## Why it exists

Running a powerful local agent and then having to babysit it at your desk
defeats the point. With aurex you get all the privacy and capability of a
locally-run agent, plus the "I can step away from my desk" affordance
that hosted services give you for free.

---

## Architecture (the polished bits)

- **One PTY per session, owned by the server.** WebSockets are
  subscribers, not attachers — disconnect, refresh, switch devices and
  the tmux session stays exactly where it was.
- **Cursor-protocol streaming.** Each session has a 2 MiB ring buffer
  with a monotonic byte cursor (modeled on
  [opencode](https://github.com/sst/opencode)'s design). Clients pass
  their last cursor on reconnect and get only what they missed.
- **Ghostty-web renderer.** libghostty compiled to WASM — real VT100,
  grapheme handling, the works. Not xterm.js.
- **Tailscale-issued real cert.** When Tailscale is present aurex pulls
  a Let's Encrypt cert via `tailscale cert` for the magic-DNS hostname
  and auto-renews. No self-signed cert nonsense.

---

## Why Tailscale (the cert story)

The only thing in aurex that needs HTTPS is **web push** — browsers
won't deliver push notifications to an insecure origin. Without push you
still get a working terminal, just no buzz on your phone when an agent
asks you something.

Aurex deliberately doesn't generate self-signed certs anymore. Installing
a self-signed CA on a phone is a 12-step Android Settings ritual that
half the time still doesn't work. Tailscale gives you:

1. A real, browser-trusted cert (free) for a stable hostname.
2. Remote access from anywhere — your phone reaches your laptop over the
   tailnet whether you're at home, at a coffee shop, or on cellular.

For the "let me check on my agent from anywhere" use case, you wanted
something like Tailscale on your phone anyway. Aurex just leans into it.

If you really want to run on plain LAN HTTP without push notifications,
set `"tailscale": "off"` in the config.

---

## Quickstart

### Prereqs
- Linux or macOS server (something with tmux + Unix PTY).
- `tmux` (`apt install tmux`, `dnf install tmux`, `brew install tmux`).
- [Tailscale](https://tailscale.com) on the server and on every device
  you want to reach it from.

### One-time Tailscale setup
```bash
sudo tailscale set --operator=$USER     # let aurex fetch certs unprivileged
```
Then enable HTTPS in the admin console:
<https://login.tailscale.com/admin/dns> → "Enable HTTPS…".

### Run from a release binary
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

To trigger the aura/push without relying on output regex, agents can
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

First run writes `aurex.config.json` in the working directory. Defaults
shown below; VAPID push keys are generated and persisted on first run
(**don't regenerate** — that invalidates every push subscription):

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

`tailscale` accepts `"auto"` (default; use Tailscale if available, fall
back to plain HTTP), `"on"` (require Tailscale, refuse to start without
it), or `"off"` (skip TLS, HTTP only — push won't work).

---

## Status

**v0.1.0** — early, single-author, self-hosted. A SaaS variant with a
hosted relay is on the medium-term roadmap; this OSS binary will always
be free and MIT.

## License

MIT. See [LICENSE](./LICENSE).
