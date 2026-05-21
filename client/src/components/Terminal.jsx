import { useEffect, useReducer, useRef, useState } from 'react';
import { FitAddon, Ghostty, Terminal as GhosttyTerminal } from 'ghostty-web';

// Diagnostic overlay for mobile input events — opt in via ?debug=input. Keeps
// the plumbing around so a future regression can be inspected without a code
// change; just navigate to /?debug=input on the device that's misbehaving.
const DEBUG_INPUT = typeof window !== 'undefined'
  && new URLSearchParams(window.location.search).get('debug') === 'input';

const THEME = {
  background: '#0b0f14',
  foreground: '#e5e7eb',
  cursor: '#22d3ee',
  cursorAccent: '#0b0f14',
  selectionBackground: 'rgba(34, 211, 238, 0.25)',
  black: '#1f2937',
  red: '#f87171',
  green: '#4ade80',
  yellow: '#fbbf24',
  blue: '#60a5fa',
  magenta: '#c084fc',
  cyan: '#22d3ee',
  white: '#e5e7eb',
  brightBlack: '#374151',
  brightRed: '#fca5a5',
  brightGreen: '#86efac',
  brightYellow: '#fde68a',
  brightBlue: '#93c5fd',
  brightMagenta: '#d8b4fe',
  brightCyan: '#67e8f9',
  brightWhite: '#f9fafb',
};

// ghostty-web requires a one-time async WASM load that produces a Ghostty
// instance. That instance must be passed into every Terminal we construct
// (via the `ghostty:` option) — without it ghostty's WASM input pipeline
// isn't wired to the Terminal and keystrokes are silently dropped. The
// top-level init() function in ghostty-web's README is NOT sufficient; we
// have to go through Ghostty.load() and thread the instance through.
let ghosttyReady;
const loadGhostty = () => {
  if (!ghosttyReady) {
    ghosttyReady = Ghostty.load().catch((err) => {
      ghosttyReady = undefined;
      throw err;
    });
  }
  return ghosttyReady;
};

/**
 * Terminal renders Ghostty's VT100 emulator (via WASM) in a canvas. Same
 * onData/onResize/scrollLines API as the xterm.js version that lived here
 * before — see git history for the swap. Touch drag → term.scrollLines() so
 * mobile gets native scrollback flick.
 */
export default function Terminal({ onReady, onInput, onResize }) {
  const containerRef = useRef(null);
  const termRef = useRef(null);
  const fitRef = useRef(null);
  const [followBottom, setFollowBottom] = useState(true);
  // Mirror of followBottom for use inside the once-at-mount effect, which can't
  // see state updates through its own stale closure.
  const followRef = useRef(true);

  // Mobile input strategy: a hidden HTML textarea overlays the terminal and
  // captures all typing. The browser handles autocorrect / swipe / IME / paste
  // natively inside the textarea, so we never have to interpret individual
  // beforeinput events (which differ across keyboards). After each change we
  // diff the textarea's current value against what we've already sent to the
  // server and emit only the delta — backspaces for removed characters, then
  // the new characters. This is keyboard-agnostic.
  const mirrorRef = useRef(null);
  const lastSentRef = useRef('');
  const [isTouch, setIsTouch] = useState(false);
  useEffect(() => {
    if (typeof window === 'undefined') return;
    const touch = ('ontouchstart' in window) || (navigator.maxTouchPoints || 0) > 0;
    setIsTouch(touch);
  }, []);

  const handleMirrorInput = (e) => {
    const ta = mirrorRef.current;
    if (!ta) return;
    const newVal = ta.value;
    const oldVal = lastSentRef.current;
    // Longest common prefix
    let i = 0;
    const minLen = Math.min(oldVal.length, newVal.length);
    while (i < minLen && oldVal.charCodeAt(i) === newVal.charCodeAt(i)) i++;
    const backspaces = oldVal.length - i;
    const additions = newVal.slice(i);
    pushDbg('mi', { it: e?.nativeEvent?.inputType, ol: oldVal.length, nl: newVal.length, bs: backspaces, ad: additions.slice(0, 8) });
    if (backspaces > 0 || additions) {
      let payload = '';
      if (backspaces > 0) payload = '\x7f'.repeat(backspaces);
      payload += additions;
      onInputRef.current?.(payload);
    }
    lastSentRef.current = newVal;
  };

  // Native beforeinput listener — installed via useEffect below so we get the
  // real DOM event (React's synthetic onBeforeInput has historically been
  // unreliable across versions). Mainly catches word-backward / line-backward
  // deletes from soft keyboards that wouldn't fire a per-character Backspace
  // keydown.
  useEffect(() => {
    const ta = mirrorRef.current;
    if (!ta || !isTouch) return undefined;
    const onBI = (e) => {
      pushDbg('mbi', { it: e.inputType, data: e.data });
      if (e.inputType === 'deleteWordBackward' || e.inputType === 'deleteSoftLineBackward') {
        // Word/line deletes don't map cleanly to a single backspace, and the
        // input event that follows will compute the correct diff for what
        // was removed from the textarea's value. If the textarea was empty
        // (server-side content), we send one backspace as a best effort.
        if (ta.value.length === 0) {
          onInputRef.current?.('\x7f');
        }
        // For non-empty: let default delete the text; input handler diffs.
      }
    };
    ta.addEventListener('beforeinput', onBI);
    return () => ta.removeEventListener('beforeinput', onBI);
  }, [isTouch]);

  const handleMirrorKeyDown = (e) => {
    pushDbg('mkd', { key: e.key, code: e.code });
    // Submit line on Enter (without shift). Send \r, drop our buffer.
    if (e.key === 'Enter' && !e.shiftKey && !e.ctrlKey && !e.metaKey && !e.altKey) {
      e.preventDefault();
      onInputRef.current?.('\r');
      if (mirrorRef.current) mirrorRef.current.value = '';
      lastSentRef.current = '';
      return;
    }
    // Backspace: always send a delete to the server, regardless of whether
    // our local textarea has any characters. This handles the common case
    // where the server's input prompt (e.g. Claude Code's input box) already
    // has text from before this client mounted — the user wants to delete
    // those characters but our mirror is empty, so the default action would
    // be a no-op. We preventDefault and do our own delete so the default
    // doesn't double-fire (which would cause two backspaces).
    if (e.key === 'Backspace' && !e.ctrlKey && !e.metaKey && !e.altKey) {
      e.preventDefault();
      onInputRef.current?.('\x7f');
      const ta = mirrorRef.current;
      if (ta && ta.value.length > 0) {
        ta.value = ta.value.slice(0, -1);
      }
      if (lastSentRef.current.length > 0) {
        lastSentRef.current = lastSentRef.current.slice(0, -1);
      }
      return;
    }
    // Everything else (printable chars, IME) flows through the input event
    // after the textarea updates. Special keys like Escape / arrows / Tab /
    // Ctrl-* are routed via the mobile toolbar.
  };

  const dbgLogRef = useRef([]);
  const [, dbgTick] = useReducer((x) => (x + 1) & 0xffff, 0);
  const pushDbg = (kind, info) => {
    if (!DEBUG_INPUT) return;
    const entry = { t: Date.now() % 100000, kind, ...info };
    const next = dbgLogRef.current.slice(-19);
    next.push(entry);
    dbgLogRef.current = next;
    dbgTick();
  };

  // Stale-closure bridge: onInput changes when ctrlArmed flips, and ghostty's
  // onData is registered once at mount. Without the ref the CTRL toolbar key
  // would never transform the next keystroke.
  const onInputRef = useRef(onInput);
  const onResizeRef = useRef(onResize);
  useEffect(() => { onInputRef.current = onInput; }, [onInput]);
  useEffect(() => { onResizeRef.current = onResize; }, [onResize]);

  const setFollow = (v) => {
    followRef.current = v;
    setFollowBottom(v);
  };

  useEffect(() => {
    if (!containerRef.current) return undefined;

    let disposed = false;
    const cleanups = [];

    const isDesktop = window.matchMedia('(min-width: 768px)').matches;
    const fontSize = isDesktop ? 15 : 14;

    loadGhostty()
      .then((ghostty) => {
        if (disposed || !containerRef.current) return;

        // Defensive: empty the container before ghostty appends its canvas +
        // textarea. If a previous ghostty instance didn't fully clean up on
        // dispose (or HMR replaced this effect), leftover DOM would stack
        // behind the new one — the "old session showing through new session"
        // bug on session switch.
        while (containerRef.current.firstChild) {
          containerRef.current.removeChild(containerRef.current.firstChild);
        }

        const term = new GhosttyTerminal({
          ghostty, // critical — wires WASM input pipeline into the Terminal
          theme: THEME,
          fontFamily: 'JetBrains Mono, Fira Code, Menlo, ui-monospace, monospace',
          fontSize,
          cursorBlink: true,
          scrollback: 50000,
        });
        const fit = new FitAddon();
        term.loadAddon(fit);
        term.open(containerRef.current);
        try { fit.fit(); } catch {}
        term.focus();

        termRef.current = term;
        fitRef.current = fit;

        const dataDispose = term.onData((data) => onInputRef.current?.(data));
        const resizeDispose = term.onResize(({ cols, rows }) => onResizeRef.current?.(cols, rows));
        cleanups.push(() => dataDispose.dispose?.());
        cleanups.push(() => resizeDispose.dispose?.());

        // Follow-bottom logic. Ghostty auto-scrolls to bottom on every write,
        // which on mobile means streaming output yanks the viewport back to
        // the bottom every time the user tries to scroll up to read history
        // or see the top of an option picker. We suppress that by saving
        // viewportY before each write and restoring it after, offset by any
        // lines pushed into scrollback. Follow-mode re-arms when the user
        // touch-scrolls back to the bottom or taps the jump-to-bottom chip.
        //
        // viewportY semantics in ghostty-web: number of lines scrolled up from
        // the bottom; 0 = at bottom.
        // Strip control sequences that would otherwise damage scrollback —
        // some TUI redraw paths emit \x1b[3J (erase scrollback) or alt-screen
        // enter/exit sequences as part of a full repaint. We don't want any of
        // those touching ghostty's history, since on mobile the only way the
        // user can re-read a long response is to scroll back through it.
        // Stripping is conservative: we keep all other ANSI behavior intact.
        const stripDangerousAnsi = (s) => {
          if (typeof s !== 'string') return s;
          return s
            .replace(/\x1b\[3J/g, '')      // erase entire scrollback
            .replace(/\x1b\[\?47[hl]/g, '')  // legacy alt-screen
            .replace(/\x1b\[\?1047[hl]/g, '') // alt-screen w/ save/restore
            .replace(/\x1b\[\?1049[hl]/g, ''); // alt-screen w/ cursor + erase
        };

        // Critical: ghostty's term.write() unconditionally calls
        // scrollToBottom() when viewportY > 0, which fires onScroll(0). If we
        // don't suppress that, our onScroll listener re-arms follow-mode and
        // we lose the user's scroll position on the very next write. So hold
        // the suppress flag across the entire write + restore window.
        let suppressScrollEvent = false;
        const writeAndPreserveScroll = (data) => {
          const cleaned = stripDangerousAnsi(data);
          if (followRef.current) {
            term.write(cleaned);
            return;
          }
          const prevY = term.viewportY;
          const prevLen = term.getScrollbackLength?.() ?? 0;
          suppressScrollEvent = true;
          try {
            term.write(cleaned);
            const newLen = term.getScrollbackLength?.() ?? 0;
            const added = Math.max(0, newLen - prevLen);
            const targetY = prevY + added;
            // scrollToLine(N) clamps N to [0, scrollbackLength] and sets
            // viewportY = N. Direct viewportY assignment bypasses the render
            // pipeline; scrollToLine fires the proper events.
            try { term.scrollToLine(targetY); } catch {}
          } finally {
            suppressScrollEvent = false;
          }
        };

        const scrollDispose = term.onScroll?.((y) => {
          if (suppressScrollEvent) return;
          // User-initiated scroll (scrollbar drag, etc). y is viewportY:
          // 0 = at bottom, > 0 = scrolled up.
          if (y === 0 && !followRef.current) setFollow(true);
          else if (y > 0 && followRef.current) setFollow(false);
        });
        if (scrollDispose) cleanups.push(() => scrollDispose.dispose?.());

        // NOTE: do NOT set tabIndex on the container. ghostty.open() puts
        // tabindex=0 + contenteditable=true on it as part of how its
        // InputHandler routes keys to WASM. Overriding either breaks input.

        // On touch devices, all typing should go through the mobile input
        // mirror (rendered above) — that's the only path that gets autocorrect
        // right. But ghostty's host is contenteditable, so a tap, focus
        // event, or programmatic call can steal focus to it. If that happens,
        // the IME starts firing events on the host instead of the mirror, and
        // typing becomes intermittently broken (the "autocorrect sometimes
        // works, sometimes doesn't" bug). Bounce focus back to the mirror
        // whenever the host receives it.
        const host = containerRef.current;
        const onHostFocusIn = () => {
          const mirror = mirrorRef.current;
          if (!mirror) return;
          if (document.activeElement === mirror) return;
          // Defer so we don't fight a focus event that's still being dispatched.
          requestAnimationFrame(() => {
            try { mirror.focus({ preventScroll: true }); } catch {
              mirror.focus();
            }
          });
        };
        // Only install the redirect on touch devices. On desktop we want
        // ghostty's native keydown path to handle typing (mirror isn't even
        // rendered there).
        const touchCapable = ('ontouchstart' in window) || (navigator.maxTouchPoints || 0) > 0;
        if (touchCapable) {
          host.addEventListener('focusin', onHostFocusIn);
          cleanups.push(() => host.removeEventListener('focusin', onHostFocusIn));
        }

        // Debounced fit — many resize bursts (orientation, keyboard, sidebar)
        // collapse to one PTY resize call. We always emit our current size via
        // onResize after fit, even if ghostty thinks the size didn't change —
        // otherwise switching devices leaves the server's PTY at the previous
        // (often smaller) client's size, since ghostty's onResize event only
        // fires on an internal dimension change.
        let resizeTimer = null;
        const emitSize = () => {
          if (term.cols > 0 && term.rows > 0) {
            onResizeRef.current?.(term.cols, term.rows);
          }
        };
        const handleResize = () => {
          if (resizeTimer) clearTimeout(resizeTimer);
          resizeTimer = setTimeout(() => {
            resizeTimer = null;
            try { fit.fit(); } catch {}
            emitSize();
          }, 100);
        };
        const refitNow = () => {
          if (resizeTimer) {
            clearTimeout(resizeTimer);
            resizeTimer = null;
          }
          try { fit.fit(); } catch {}
          emitSize();
        };
        window.addEventListener('resize', handleResize);
        window.addEventListener('orientationchange', handleResize);
        const ro = new ResizeObserver(handleResize);
        ro.observe(containerRef.current);
        cleanups.push(() => {
          if (resizeTimer) clearTimeout(resizeTimer);
          window.removeEventListener('resize', handleResize);
          window.removeEventListener('orientationchange', handleResize);
          ro.disconnect();
        });

        // Mobile touch scroll — drag finger → term.scrollLines(). ghostty's
        // canvas doesn't expose a DOM scroll target on its own, so we drive
        // scrollLines manually. Cell height estimated from fontSize × lineHeight
        // (no DOM grid to measure against, unlike the old xterm.js setup).
        let touchStartY = null;
        let touchStartX = null;
        let touchAccum = 0;
        let touchMovedTotal = 0;
        const cellHeight = fontSize * 1.2;
        const onTouchStart = (e) => {
          if (e.touches.length !== 1) return;
          touchStartY = e.touches[0].clientY;
          touchStartX = e.touches[0].clientX;
          touchAccum = 0;
          touchMovedTotal = 0;
        };
        const onTouchMove = (e) => {
          if (touchStartY === null || e.touches.length !== 1) return;
          const y = e.touches[0].clientY;
          const x = e.touches[0].clientX;
          touchMovedTotal += Math.abs(y - touchStartY) + Math.abs(x - (touchStartX ?? x));
          touchAccum += touchStartY - y; // finger up = positive accum = scroll forward
          touchStartY = y;
          touchStartX = x;
          const steps = Math.trunc(touchAccum / cellHeight);
          if (steps === 0) return;
          try { term.scrollLines(steps); } catch {}
          touchAccum -= steps * cellHeight;
          // After the scroll, update follow mode based on where we landed.
          // onScroll handles this too, but updating eagerly here avoids a
          // one-frame flicker of the chip during a quick flick.
          const atBottom = (term.viewportY ?? 0) === 0;
          if (atBottom !== followRef.current) setFollow(atBottom);
        };
        const onTouchEnd = () => {
          // Tap (small total movement) → focus the mobile input mirror so the
          // soft keyboard opens. Drags (scrolling) leave focus alone.
          const wasTap = touchMovedTotal < 8;
          touchStartY = null;
          touchStartX = null;
          touchAccum = 0;
          touchMovedTotal = 0;
          if (wasTap && mirrorRef.current) {
            try { mirrorRef.current.focus({ preventScroll: true }); } catch {
              mirrorRef.current.focus();
            }
          }
        };
        containerRef.current.addEventListener('touchstart', onTouchStart, { passive: true });
        containerRef.current.addEventListener('touchmove', onTouchMove, { passive: true });
        containerRef.current.addEventListener('touchend', onTouchEnd);
        containerRef.current.addEventListener('touchcancel', onTouchEnd);
        cleanups.push(() => {
          containerRef.current?.removeEventListener('touchstart', onTouchStart);
          containerRef.current?.removeEventListener('touchmove', onTouchMove);
          containerRef.current?.removeEventListener('touchend', onTouchEnd);
          containerRef.current?.removeEventListener('touchcancel', onTouchEnd);
        });

        // Focus the actual textarea ghostty places inside the host. On mobile,
        // term.focus() alone can fail to open the soft keyboard because the
        // browser requires the focus to land on a real input element from a
        // user gesture — direct .focus() on the textarea reliably triggers it.
        const focusTerm = () => {
          // On touch devices, focus our mobile input mirror — that's where all
          // typing should land. On desktop, fall through to ghostty's native
          // focus (its keydown handler is what processes typing there).
          if (mirrorRef.current) {
            try { mirrorRef.current.focus({ preventScroll: true }); return; } catch {}
            mirrorRef.current.focus();
            return;
          }
          const ta = term.textarea;
          if (ta && typeof ta.focus === 'function') {
            ta.focus();
          } else {
            term.focus();
          }
        };
        focusTerm();

        const jumpToBottom = () => {
          try { term.scrollToBottom(); } catch {}
          setFollow(true);
        };

        onReady?.({
          write: writeAndPreserveScroll,
          focus: focusTerm,
          sendKey: (s) => onInputRef.current?.(s),
          fit: handleResize,
          refit: refitNow,
          jumpToBottom,
        });

        // Initial size handshake so the server knows our dimensions
        // immediately, before any output arrives.
        refitNow();
      })
      .catch((err) => {
        // WASM init failure is rare but worth surfacing.
        console.error('aurex: ghostty-web init failed', err);
      });

    return () => {
      disposed = true;
      for (const fn of cleanups) {
        try { fn(); } catch {}
      }
      if (termRef.current) {
        try { termRef.current.dispose(); } catch {}
      }
      termRef.current = null;
      fitRef.current = null;
      // ghostty.dispose() doesn't always remove the appended canvas + textarea.
      // Clean them out so the host div is empty if React keeps it alive (e.g.,
      // strict mode double-mount in dev).
      if (containerRef.current) {
        while (containerRef.current.firstChild) {
          containerRef.current.removeChild(containerRef.current.firstChild);
        }
      }
    };
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, []);

  const onJumpClick = () => {
    const term = termRef.current;
    if (term) {
      try { term.scrollToBottom(); } catch {}
    }
    setFollow(true);
  };

  return (
    // No onClick handler on the inner container — ghostty installs
    // mousedown/touchend listeners on its canvas that already call
    // textarea.focus(). React's synthetic onClick here would compete with
    // ghostty's preventDefault on touchend.
    <div className="relative flex-1 min-h-0 bg-bg">
      <div ref={containerRef} className="absolute inset-0" />
      {isTouch && (
        // Hidden input mirror. pointer-events:none lets touch events fall
        // through to the container below for scrolling — the container's
        // touch-end handler is what programmatically focuses this textarea
        // on a tap, which opens the soft keyboard. Everything inside is
        // transparent so the user sees only ghostty's rendering.
        <textarea
          ref={mirrorRef}
          autoCapitalize="off"
          autoComplete="off"
          autoCorrect="on"
          spellCheck={true}
          aria-label="Terminal input"
          className="absolute inset-0 z-10 resize-none border-0 bg-transparent p-0 outline-none"
          style={{
            color: 'transparent',
            caretColor: 'transparent',
            WebkitTextFillColor: 'transparent',
            pointerEvents: 'none',
            font: 'inherit',
          }}
          onInput={handleMirrorInput}
          onKeyDown={handleMirrorKeyDown}
        />
      )}
      {DEBUG_INPUT && (
        <div className="absolute left-1 top-1 z-20 max-w-[95%] rounded border border-amber-500/50 bg-black/85 p-1.5 font-mono text-[10px] leading-tight text-amber-200">
          <div className="mb-1 flex items-center gap-2">
            <span className="text-amber-400">input debug ({dbgLogRef.current.length})</span>
            <button
              type="button"
              tabIndex={-1}
              onMouseDown={(e) => e.preventDefault()}
              onTouchStart={(e) => e.preventDefault()}
              onClick={() => {
                const txt = dbgLogRef.current.map((e) => {
                  if (e.kind === 'mi') return `mi ${e.it || '?'} ol=${e.ol} nl=${e.nl} bs=${e.bs} ad=${JSON.stringify(e.ad)}`;
                  if (e.kind === 'mbi') return `mbi ${e.it} d=${JSON.stringify(e.data)}`;
                  if (e.kind === 'mkd') return `mkd ${JSON.stringify(e.key)} (${e.code})`;
                  if (e.kind === 'bi') return `bi ${e.it} d=${JSON.stringify(e.data)} rng=${e.rng} wl=${e.wl}${e.cmp ? ' cmp' : ''}${e.rk ? ' rk' : ''}`;
                  if (e.kind === 'keydown') return `kd ${JSON.stringify(e.key)} (${e.code})`;
                  if (e.kind === 'compStart') return `compStart d=${JSON.stringify(e.data)} wl=${e.wl}`;
                  if (e.kind === 'compEnd') return `compEnd d=${JSON.stringify(e.data)} wl=${e.wl}`;
                  return JSON.stringify(e);
                }).join('\n');
                navigator.clipboard?.writeText(txt).catch(() => {});
              }}
              className="rounded border border-amber-400/60 px-1.5 py-0.5 text-amber-300 active:bg-amber-500/20"
            >
              copy
            </button>
            <button
              type="button"
              tabIndex={-1}
              onMouseDown={(e) => e.preventDefault()}
              onTouchStart={(e) => e.preventDefault()}
              onClick={() => { dbgLogRef.current = []; dbgTick(); }}
              className="rounded border border-amber-400/60 px-1.5 py-0.5 text-amber-300 active:bg-amber-500/20"
            >
              clear
            </button>
          </div>
          {dbgLogRef.current.slice(-12).map((e, i) => (
            <div key={i} className="whitespace-pre">
              {e.kind === 'mi'
                ? `mi ${e.it || '?'} ol=${e.ol} nl=${e.nl} bs=${e.bs} ad=${JSON.stringify(e.ad)}`
                : e.kind === 'mbi'
                ? `mbi ${e.it} d=${JSON.stringify(e.data)}`
                : e.kind === 'mkd'
                ? `mkd ${JSON.stringify(e.key)} (${e.code})`
                : e.kind === 'bi'
                ? `bi ${e.it} d=${JSON.stringify(e.data)} rng=${e.rng} wl=${e.wl}${e.cmp ? ' cmp' : ''}${e.rk ? ' rk' : ''}`
                : e.kind === 'keydown'
                ? `kd ${JSON.stringify(e.key)} (${e.code})`
                : e.kind === 'compStart'
                ? `compStart d=${JSON.stringify(e.data)} wl=${e.wl}`
                : e.kind === 'compEnd'
                ? `compEnd d=${JSON.stringify(e.data)} wl=${e.wl}`
                : JSON.stringify(e)}
            </div>
          ))}
        </div>
      )}
      {!followBottom && (
        <button
          type="button"
          tabIndex={-1}
          onMouseDown={(e) => e.preventDefault()}
          onTouchStart={(e) => e.preventDefault()}
          onClick={onJumpClick}
          aria-label="Jump to bottom"
          className="absolute bottom-3 right-3 z-10 flex items-center gap-1 rounded-full border border-aura/40 bg-aura/15 px-3 py-1.5 text-xs font-mono text-aura shadow-lg backdrop-blur active:bg-aura/25"
        >
          <span aria-hidden="true">↓</span>
          <span>jump to bottom</span>
        </button>
      )}
    </div>
  );
}
