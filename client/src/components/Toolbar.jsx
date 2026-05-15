import { useState } from 'react';

// Trimmed set: only the keys that physical phone keyboards don't have or are
// awkward to reach. Anything else (symbols, Pg keys, copy-mode shortcuts) is
// either typeable on the keyboard or handled by the scrollback overlay.
const KEYS = [
  { label: 'ESC', seq: '\x1b' },
  { label: 'TAB', seq: '\t' },
  { label: '↑',   seq: '\x1b[A' },
  { label: '↓',   seq: '\x1b[B' },
  { label: '←',   seq: '\x1b[D' },
  { label: '→',   seq: '\x1b[C' },
];

// When CTRL is sticky, pressing a letter sends the corresponding control code.
function ctrlOf(ch) {
  if (!ch || ch.length !== 1) return null;
  const code = ch.toLowerCase().charCodeAt(0);
  if (code < 97 || code > 122) return null;
  return String.fromCharCode(code - 96);
}

/**
 * Mobile toolbar. Buttons use tabIndex=-1 plus preventDefault on
 * mousedown/touchstart so they can never steal focus from xterm's hidden
 * textarea — the soft keyboard stays up across taps.
 */
export default function Toolbar({ onSendKey, onCtrlArm, ctrlArmed }) {
  const [pressed, setPressed] = useState(null);

  const handleTap = (k) => {
    setPressed(k.label);
    setTimeout(() => setPressed(null), 80);
    onSendKey(k.seq);
  };

  const noFocusSteal = (e) => e.preventDefault();

  return (
    <div
      className="flex shrink-0 flex-wrap items-center gap-1 border-t border-line bg-panel px-2 py-2"
      style={{ paddingBottom: 'max(0.5rem, env(safe-area-inset-bottom))' }}
    >
      <button
        tabIndex={-1}
        onMouseDown={noFocusSteal}
        onTouchStart={noFocusSteal}
        onClick={onCtrlArm}
        className={[
          'shrink-0 rounded-md border px-3 py-2 text-xs font-mono',
          ctrlArmed
            ? 'border-aura bg-aura/15 text-aura'
            : 'border-line bg-bg text-zinc-200 active:bg-zinc-800',
        ].join(' ')}
      >
        CTRL
      </button>
      {KEYS.map((k) => (
        <button
          key={k.label}
          tabIndex={-1}
          onMouseDown={noFocusSteal}
          onTouchStart={noFocusSteal}
          onClick={() => handleTap(k)}
          className={[
            'shrink-0 rounded-md border px-3 py-2 text-xs font-mono',
            pressed === k.label
              ? 'border-aura bg-aura/15 text-aura'
              : 'border-line bg-bg text-zinc-200 active:bg-zinc-800',
          ].join(' ')}
        >
          {k.label}
        </button>
      ))}
    </div>
  );
}

export { ctrlOf };
