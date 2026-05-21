import { useEffect, useRef, useState } from 'react';

/**
 * Full-screen overlay that fetches /api/sessions/:id/transcript and renders it
 * as plain text. The transcript is the raw byte stream from the PTY with ANSI
 * control sequences stripped server-side — it preserves content that Claude
 * Code's TUI redrew over (and which therefore never reached ghostty's
 * scrollback).
 *
 * Caveats the user should know:
 *   - Because TUI apps re-emit the same lines on every redraw, there will be
 *     repetition. We don't try to dedupe — anything we'd dedupe might be
 *     actual repeated content from Claude.
 *   - The buffer is bounded (16 MiB per session); very old content drops off
 *     the front.
 */
export default function TranscriptPanel({ sessionId, sessionName, onClose }) {
  const [text, setText] = useState('');
  const [loading, setLoading] = useState(true);
  const [error, setError] = useState(null);
  const [copied, setCopied] = useState(false);
  const bodyRef = useRef(null);

  useEffect(() => {
    if (!sessionId) return undefined;
    let cancelled = false;
    setLoading(true);
    setError(null);
    fetch(`/api/sessions/${sessionId}/transcript`, { credentials: 'same-origin' })
      .then((res) => {
        if (!res.ok) throw new Error(`HTTP ${res.status}`);
        return res.text();
      })
      .then((body) => {
        if (cancelled) return;
        setText(body);
        setLoading(false);
        // Scroll to bottom (newest content) by default — same convention as
        // the terminal itself.
        requestAnimationFrame(() => {
          if (bodyRef.current) {
            bodyRef.current.scrollTop = bodyRef.current.scrollHeight;
          }
        });
      })
      .catch((err) => {
        if (cancelled) return;
        setError(err.message || 'failed to load transcript');
        setLoading(false);
      });
    return () => {
      cancelled = true;
    };
  }, [sessionId]);

  const handleCopy = () => {
    if (!text) return;
    navigator.clipboard?.writeText(text).then(
      () => {
        setCopied(true);
        setTimeout(() => setCopied(false), 1500);
      },
      () => {}
    );
  };

  const handleJumpTop = () => {
    if (bodyRef.current) bodyRef.current.scrollTop = 0;
  };
  const handleJumpBottom = () => {
    if (bodyRef.current) bodyRef.current.scrollTop = bodyRef.current.scrollHeight;
  };

  return (
    <div
      className="fixed inset-0 z-50 flex flex-col bg-bg"
      style={{ paddingBottom: 'env(safe-area-inset-bottom)' }}
    >
      <header className="flex shrink-0 items-center gap-2 border-b border-line bg-panel px-3 py-2">
        <button
          type="button"
          onClick={onClose}
          className="rounded px-2 py-1 text-sm text-zinc-300 hover:bg-zinc-800 active:bg-zinc-700"
          aria-label="Close transcript"
        >
          ← back
        </button>
        <div className="min-w-0 flex-1 truncate text-sm font-medium text-zinc-100">
          transcript{sessionName ? ` — ${sessionName}` : ''}
        </div>
        <button
          type="button"
          onClick={handleJumpTop}
          className="rounded px-2 py-1 text-xs font-mono text-zinc-300 hover:bg-zinc-800"
          title="Jump to top"
        >
          ↑ top
        </button>
        <button
          type="button"
          onClick={handleJumpBottom}
          className="rounded px-2 py-1 text-xs font-mono text-zinc-300 hover:bg-zinc-800"
          title="Jump to bottom"
        >
          ↓ end
        </button>
        <button
          type="button"
          onClick={handleCopy}
          disabled={!text}
          className="rounded border border-aura/40 bg-aura/10 px-2 py-1 text-xs font-mono text-aura disabled:opacity-40 active:bg-aura/25"
        >
          {copied ? 'copied' : 'copy'}
        </button>
      </header>

      <div ref={bodyRef} className="flex-1 overflow-auto bg-bg">
        {loading && (
          <div className="p-4 text-sm text-zinc-500">Loading transcript…</div>
        )}
        {error && (
          <div className="p-4 text-sm text-red-300">
            Couldn’t load transcript: {error}
          </div>
        )}
        {!loading && !error && (
          <pre className="m-0 whitespace-pre-wrap break-words p-3 font-mono text-[12px] leading-snug text-zinc-200">
            {text || <span className="text-zinc-500">(empty)</span>}
          </pre>
        )}
      </div>
    </div>
  );
}
