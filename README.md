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

## Three modes

Aurex runs in one of three modes depending on how much friction you'll
trade for push notifications.

| | **LAN HTTP** | **LAN HTTPS (self-signed)** | **Tailscale** |
|---|---|---|---|
| Terminal | `http://host:7681` | `https://host:7681` | `https://host.tailnet.ts.net:7681` |
| Reach from outside LAN | ✗ | ✗ | ✓ |
| Web push to phone | ✗ | ✓ | ✓ |
| Setup | none beyond `tmux` | install aurex CA on each phone once | Tailscale on every device |
| Cert | none | self-signed, auto-generated | real Let's Encrypt, auto-renewing |

Push notifications are the only feature gated on HTTPS — browsers
refuse Service Worker registration outside a secure context. If you
don't need a buzz when an agent asks something, **LAN HTTP** is the
zero-friction option. If you want push without a Tailscale account,
**self-signed** works after a one-time CA install on each phone. If
you want push *and* access from outside your LAN, **Tailscale** is
the cleanest path.

---

## Quickstart

### Prereqs (all modes)
- Linux, macOS, or Windows-via-WSL2 (anything with `tmux` + Unix PTY).
  Native Windows isn't supported — there's no tmux. WSL2 works fine.
- `tmux` **3.0 or newer** (`apt install tmux`, `dnf install tmux`,
  `brew install tmux`). Aurex uses `set-option … status off`,
  `allow-passthrough`, and `pipe-pane -o`, all of which need 3.0+.
- `git` on `$PATH`. The sidebar's branch indicator shells out to it.
- A modern browser: Chrome/Edge 90+, Firefox 90+, Safari 16.4+. Push
  needs Service Workers + PushManager (Safari iOS additionally needs
  the site installed as a PWA — see **Mobile** below).
- **Linux firewall**: most distros (Fedora, Ubuntu Server) block port
  `7681` by default. Open it once:
  ```bash
  sudo firewall-cmd --add-port=7681/tcp --permanent && sudo firewall-cmd --reload   # firewalld
  sudo ufw allow 7681/tcp                                                            # ufw
  ```

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

### LAN HTTPS — self-signed (push on LAN without Tailscale)

Aurex generates a self-signed CA cert on first run if `tls: true` (the
default). The cert covers `localhost` plus every non-loopback IP on the
host, so the same cert works for desktop and phone access.

```bash
./aurex
# log: aurex: listening on https://0.0.0.0:7681 (self-signed — install
#      aurex.cert.pem on phone to avoid warnings)
```

On each phone you want push on, install the cert as a **CA certificate**
(not a user/client certificate — that path asks for a private key):

1. In aurex, open the **Notifications** panel (sidebar → notifications
   button). Tap **Download cert**. The browser saves `aurex.crt`.
2. **Don't tap the file in Downloads** — that often triggers the wrong
   install flow ("private key needed"). Open Android *Settings →
   Security & privacy → Encryption & credentials → Install a certificate
   → CA certificate*. iOS: *Settings → General → VPN & Device
   Management → install profile, then enable in Cert Trust Settings*.
3. Reload aurex. Push should now work end-to-end (panel shows
   `SW registration: ready`).

If your phone is on Chrome Android and you don't want to install a CA,
the **Chrome flag**
`chrome://flags/#unsafely-treat-insecure-origin-as-secure` accepts
`https://<host>:7681` as a temporary workaround — but it only takes
`isSecureContext` to `true`, it doesn't fix TLS, so Service Worker
registration still fails. Install the cert.

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
Build prereqs: **Go 1.22+** and **Node 18+**. (Vite 5 requires Node 18.)
```bash
git clone git@github.com:gbarnh/aurex.git
cd aurex/client && npm ci && npm run build
cd .. && go build -o aurex .
./aurex
```
The React build output (`client/dist/`) is embedded into the binary via
`go:embed`, so `aurex` is a single static file you can move to another
machine of the same OS/arch and run.

---

## Agent hooks

To trigger the aura and push without relying on output regex, agents
POST to a localhost endpoint with the **tmux session name** they're
running inside:

```bash
curl -s -X POST http://localhost:7681/api/hook/aura \
  -H 'Content-Type: application/json' \
  -d "{\"active\": true, \"reason\": \"Claude is waiting for input\", \"session\": \"$(tmux display-message -p '#S')\"}"
```

The `session` field is required when multiple sessions exist — the
server needs to know which session's ring to glow. `tmux display-message
-p '#S'` returns the current tmux session name (e.g. `aurex-958dfcfb`)
when the command runs inside one, which is exactly what aurex uses
internally.

For Claude Code, add to `.claude/settings.json`:

```json
{
  "hooks": {
    "Stop": [{
      "matcher": "",
      "hooks": [{
        "type": "command",
        "command": "curl -s -X POST http://localhost:7681/api/hook/aura -H 'Content-Type: application/json' -d \"{\\\"active\\\":true,\\\"reason\\\":\\\"Claude is waiting for input\\\",\\\"session\\\":\\\"$(tmux display-message -p '#S' 2>/dev/null)\\\"}\""
      }]
    }]
  }
}
```

If the hook is missing or has no session field and you have multiple
sessions, aurex logs `hook: no session match for "" (active sessions:
[...])`. Use that to verify the names you should send.

---

## Mobile

Open the aurex URL on your phone. Tap **Notifications** in the
sidebar → **Enable** → grant the system prompt → **Send test**.

The toolbar below the terminal has CTRL / ESC / TAB / arrows — the keys
phone soft keyboards lack. CTRL is sticky-once: tap CTRL, then a letter
to send the control code.

### iPhone (Safari) specifics
iOS Safari has hard requirements for web push that no setting flips:

- **iOS 16.4 or newer** (Apple shipped web push in 16.4).
- **The site must be installed as a PWA first.** Web push only works
  from a Home-Screen icon, not from a Safari tab. Open aurex in
  Safari → Share → **Add to Home Screen** → open it from the icon →
  *then* enable notifications.
- This works cleanly on the Tailscale path (real cert). On self-signed
  LAN HTTPS, Safari's "Add to Home Screen" tends to refuse certs the
  user manually trusted — Tailscale is much smoother on iOS.

Android Chrome doesn't need PWA install — enabling from the regular tab
works as long as the origin is a secure context.

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

## Security

Aurex ships with `auth: false`. **Anyone who can reach the port gets a
root-equivalent shell on your machine** — the WebSocket attaches to
real `tmux` sessions running as the user who started `aurex`.

For LAN-only setups behind a trusted firewall this is the same trust
model as SSH on the local network: usually fine. **Don't expose aurex
to the public internet on `auth: false`.** Tailscale Funnel, port
forwarding, reverse proxies — any of those without auth turned on is a
foot-gun.

To enable basic auth, edit `aurex.config.json`:
```json
{
  "auth": true,
  "username": "you",
  "password": "something-long-and-random"
}
```
Restart. The browser will prompt for credentials; agents calling the
`/api/hook/aura` endpoint don't need them (the hook is gated to
localhost regardless).

For a stronger story you can put aurex behind an authenticating
reverse proxy (Caddy with `basic_auth` / `forward_auth`, nginx with
`auth_request`, Cloudflare Access in front of Tailscale Funnel, etc.).
The WebSocket upgrade flows through chi's stdlib handlers, so any proxy
that handles WS works.

---

## Updating

Aurex is a single binary. To upgrade:
```bash
pkill -f /aurex/aurex
curl -fsSL https://github.com/gbarnh/aurex/releases/latest/download/aurex-linux-amd64 -o aurex
chmod +x aurex && ./aurex
```
Your existing `aurex.config.json`, certs, VAPID keys, and push
subscriptions are preserved. Existing `tmux` sessions are adopted on
startup — your in-flight agent work survives the upgrade.

If you change VAPID keys (deleting the keys from the config and
restarting), every phone needs to re-subscribe via the **Unsubscribe**
button in the push panel, then **Enable** again.

---

## Troubleshooting

**`push: test → total=0 sent=0`**
No subscriptions stored. Tap **Enable** in the push panel first.
Confirm "Subscribed: yes" in the diagnostic grid before sending a test.

**`push: test → total=N sent=0 failed=N lastErr="push service returned 403"`**
FCM rejected the VAPID JWT. Almost always means the browser is using a
cached subscription bound to a previous VAPID key. Tap **Unsubscribe**
in the push panel, then **Enable** — this forces a fresh subscription
against the current key.

**Push panel shows `Secure context: yes` but `SW registration: timeout`**
Chrome flag (`unsafely-treat-insecure-origin-as-secure`) marks the
origin as secure but doesn't fix TLS — the Service Worker fetch still
fails over the bypassed cert. Install the cert as a CA (see
[LAN HTTPS — self-signed](#lan-https--self-signed-push-on-lan-without-tailscale)),
or switch to Tailscale.

**Android cert install asks for a private key**
You're in the wrong installer. *Settings → Security & privacy →
Encryption & credentials → Install a certificate → **CA certificate***.
Don't tap the file from Downloads — that opens the user-cert flow which
demands a key aurex isn't (and shouldn't be) handing out.

**`aurex: serve: listen tcp :7681: bind: address already in use`**
A prior aurex (or `/aurex` zombie) is holding the port:
```bash
ss -tlnp | grep 7681          # find PID
kill -9 <pid>                 # then restart aurex
```

**Aurex starts but `tailscale cert` fails with "Access denied"**
You skipped the operator step. Once:
```bash
sudo tailscale set --operator=$USER
```
And confirm HTTPS is enabled in the admin console
(<https://login.tailscale.com/admin/dns>).

**Mobile keyboard dismisses when I tap CTRL / arrows**
Fixed in v0.1.0. If you still see it on a stale build, hard-refresh.
The toolbar buttons use `preventDefault` + `tabindex=-1` so they
literally can't take focus from xterm's hidden textarea.

**Switching sessions shows the previous session's content**
Fixed in v0.1.0. If you still see overlapping content, your tab is
running an old bundle — hard-refresh.

---

## Status

**v0.1.0.** A SaaS variant with a hosted relay is on the roadmap; this
OSS binary will always be free and MIT.

## License

MIT. See [LICENSE](./LICENSE).
