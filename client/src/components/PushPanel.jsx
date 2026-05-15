import { useState } from 'react';

function Row({ k, v, ok }) {
  return (
    <div className="flex items-center justify-between gap-3 py-1 text-xs">
      <span className="text-zinc-400">{k}</span>
      <span className={ok ? 'text-emerald-400' : 'text-amber-400'}>{v}</span>
    </div>
  );
}

/**
 * PushPanel exposes the state of the web-push pipeline so the user can see
 * exactly which step fails when notifications aren't working. Especially
 * useful on Chrome Android with self-signed certs where isSecureContext
 * silently disqualifies the page.
 */
export default function PushPanel({ push, onClose }) {
  const [busy, setBusy] = useState(false);
  const [testStatus, setTestStatus] = useState('');

  const handleEnable = async () => {
    setBusy(true);
    setTestStatus('');
    try {
      await push.subscribe();
    } finally {
      setBusy(false);
    }
  };

  const handleTest = async () => {
    setTestStatus('sending…');
    try {
      const res = await fetch('/api/push/test', { method: 'POST' });
      if (!res.ok) {
        setTestStatus(`HTTP ${res.status}`);
        return;
      }
      const body = await res.json();
      if (body.total === 0) {
        setTestStatus('0 subscribers on the server — tap Enable first');
      } else if (body.sent > 0) {
        setTestStatus(
          `delivered to ${body.sent}/${body.total} (statuses: ${(body.statuses || []).join(', ') || 'n/a'})`
        );
      } else {
        setTestStatus(
          `0 delivered of ${body.total}; failed=${body.failed}, pruned=${body.pruned}, lastErr=${body.lastError || 'none'}`
        );
      }
    } catch (e) {
      setTestStatus(String(e?.message || e));
    }
  };

  return (
    <div className="fixed inset-0 z-50 flex items-end justify-center bg-black/60 md:items-center">
      <div className="w-full max-w-sm rounded-t-xl border border-line bg-panel p-4 md:rounded-xl">
        <div className="mb-3 flex items-center justify-between">
          <div className="text-base font-semibold">Push notifications</div>
          <button onClick={onClose} className="rounded p-1 text-zinc-400 hover:bg-zinc-800">✕</button>
        </div>

        <div className="rounded-md border border-line bg-bg/50 px-3 py-2">
          <Row k="Secure context" v={push.isSecure ? 'yes' : 'no'} ok={push.isSecure} />
          <Row k="ServiceWorker API" v={push.hasSW ? 'yes' : 'no'} ok={push.hasSW} />
          <Row k="PushManager API" v={push.hasPush ? 'yes' : 'no'} ok={push.hasPush} />
          <Row k="Notification API" v={push.hasNotif ? 'yes' : 'no'} ok={push.hasNotif} />
          <Row k="SW registration" v={push.swState} ok={push.swState === 'ready'} />
          <Row k="Permission" v={push.permission} ok={push.permission === 'granted'} />
          <Row k="Subscribed" v={push.subscribed ? 'yes' : 'no'} ok={push.subscribed} />
        </div>

        {push.lastError && (
          <div className="mt-2 break-words rounded-md border border-red-500/30 bg-red-500/10 px-3 py-2 text-xs text-red-300">
            {push.lastError}
          </div>
        )}

        {push.swState === 'timeout' && (
          <div className="mt-3 rounded-md border border-line bg-bg/50 px-3 py-2 text-xs leading-relaxed text-zinc-300">
            <div className="mb-1 font-medium text-zinc-100">Service worker registration timed out.</div>
            Chrome treats the page as secure (via flag) but still refuses to run a Service Worker
            over a cert it doesn't actually trust. Install the cert as a CA on this phone:
            <ol className="mt-1 list-decimal space-y-1 pl-4 text-zinc-400">
              <li>Tap <strong>Download cert</strong> below. File will be <code>aurex.crt</code>.</li>
              <li>
                <strong>Don't tap the file from Downloads</strong> — that opens the wrong install
                flow ("private key required"). Instead, open Android <em>Settings → Security &amp;
                privacy → More security &amp; privacy → Encryption &amp; credentials → Install a
                certificate</em>.
              </li>
              <li>
                Choose <strong>CA certificate</strong> (not "User certificate" / "VPN &amp; app
                user certificate"). Accept the "network may be monitored" warning.
              </li>
              <li>Pick <code>aurex.crt</code> from Downloads.</li>
              <li>Reload aurex — SW registration should succeed, then tap <strong>Enable</strong>.</li>
            </ol>
            <a
              href="/aurex.cert.pem"
              download="aurex.crt"
              className="mt-2 inline-block rounded-md border border-aura/40 bg-aura/10 px-3 py-1.5 text-aura"
            >
              Download cert
            </a>
          </div>
        )}

        {!push.isSecure && (
          <div className="mt-3 rounded-md border border-line bg-bg/50 px-3 py-2 text-xs leading-relaxed text-zinc-300">
            <div className="mb-1 font-medium text-zinc-100">Page is not a secure context.</div>
            Chrome Android refuses push on self-signed certs. Fix one of:
            <ul className="mt-1 list-disc space-y-1 pl-4 text-zinc-400">
              <li>
                Open <code className="text-aura">chrome://flags/#unsafely-treat-insecure-origin-as-secure</code>,
                add this origin, Enabled, Relaunch.
              </li>
              <li>Install <code className="text-aura">aurex.cert.pem</code> as a CA in Android settings.</li>
              <li>Front aurex with Tailscale serve / a real cert.</li>
            </ul>
          </div>
        )}

        <div className="mt-3 flex gap-2">
          <button
            onClick={handleEnable}
            disabled={busy || push.subscribed}
            className="flex-1 rounded-md border border-aura/40 bg-aura/10 px-3 py-2 text-sm text-aura hover:bg-aura/20 disabled:opacity-50"
          >
            {push.subscribed ? 'Subscribed' : busy ? 'Working…' : 'Enable'}
          </button>
          <button
            onClick={handleTest}
            disabled={!push.subscribed}
            className="flex-1 rounded-md border border-line bg-bg px-3 py-2 text-sm text-zinc-200 hover:border-aura/40 disabled:opacity-50"
          >
            Send test
          </button>
        </div>
        {push.subscribed && (
          <button
            onClick={push.unsubscribe}
            className="mt-2 w-full rounded-md border border-line bg-bg px-3 py-1.5 text-xs text-zinc-400 hover:border-red-500/40 hover:text-red-300"
          >
            Unsubscribe (useful if push keeps 403'ing)
          </button>
        )}
        {testStatus && <div className="mt-2 text-xs text-zinc-400">{testStatus}</div>}
      </div>
    </div>
  );
}
