import { useEffect, useRef } from 'react';
import { FitAddon, Ghostty, Terminal as GhosttyTerminal } from 'ghostty-web';

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

  // Stale-closure bridge: onInput changes when ctrlArmed flips, and ghostty's
  // onData is registered once at mount. Without the ref the CTRL toolbar key
  // would never transform the next keystroke.
  const onInputRef = useRef(onInput);
  const onResizeRef = useRef(onResize);
  useEffect(() => { onInputRef.current = onInput; }, [onInput]);
  useEffect(() => { onResizeRef.current = onResize; }, [onResize]);

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
          scrollback: 10000,
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

        // NOTE: do NOT set tabIndex on the container. ghostty.open() puts
        // tabindex=0 + contenteditable=true on it as part of how its
        // InputHandler routes keys to WASM. Overriding either breaks input.

        // Mobile soft-keyboard input fix.
        //
        // ghostty-web 0.4 only routes characters via the keydown path. Most
        // Android soft keyboards (Gboard especially) don't fire keydown for
        // printable characters — they only fire beforeinput / input. Ghostty
        // blocks beforeinput with preventDefault but never reads e.data, so
        // mobile typing silently drops every character.
        //
        // Workaround: read e.data ourselves on beforeinput and send it via
        // onInput. Dedupe by checking whether a recent keydown carried a real
        // key — when it did (desktop / hardware keyboard) ghostty already
        // handled it; when it didn't (mobile) we send.
        const host = containerRef.current;
        let lastKeyAt = 0;
        let lastKeyHadValue = false;
        const onKeyDownCap = (e) => {
          lastKeyAt = performance.now();
          lastKeyHadValue = !!(e.key && e.key.length === 1 && !e.ctrlKey && !e.metaKey && !e.altKey);
        };
        const onBeforeInput = (e) => {
          const recentRealKey = performance.now() - lastKeyAt < 50 && lastKeyHadValue;
          if (recentRealKey) return;
          switch (e.inputType) {
            case 'insertText':
            case 'insertFromPaste':
            case 'insertReplacementText':
              if (e.data) onInputRef.current?.(e.data);
              break;
            case 'insertLineBreak':
            case 'insertParagraph':
              onInputRef.current?.('\r');
              break;
            case 'deleteContentBackward':
              onInputRef.current?.('\x7f');
              break;
            case 'deleteContentForward':
              onInputRef.current?.('\x1b[3~');
              break;
            // insertCompositionText is handled by ghostty's compositionend path.
          }
        };
        host.addEventListener('keydown', onKeyDownCap, true);
        host.addEventListener('beforeinput', onBeforeInput);
        cleanups.push(() => {
          host.removeEventListener('keydown', onKeyDownCap, true);
          host.removeEventListener('beforeinput', onBeforeInput);
        });

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
        let touchAccum = 0;
        const cellHeight = fontSize * 1.2;
        const onTouchStart = (e) => {
          if (e.touches.length !== 1) return;
          touchStartY = e.touches[0].clientY;
          touchAccum = 0;
        };
        const onTouchMove = (e) => {
          if (touchStartY === null || e.touches.length !== 1) return;
          const y = e.touches[0].clientY;
          touchAccum += touchStartY - y; // finger up = positive accum = scroll forward
          touchStartY = y;
          const steps = Math.trunc(touchAccum / cellHeight);
          if (steps === 0) return;
          try { term.scrollLines(steps); } catch {}
          touchAccum -= steps * cellHeight;
        };
        const onTouchEnd = () => {
          touchStartY = null;
          touchAccum = 0;
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
          const ta = term.textarea;
          if (ta && typeof ta.focus === 'function') {
            ta.focus();
          } else {
            term.focus();
          }
        };
        focusTerm();

        onReady?.({
          write: (s) => term.write(s),
          focus: focusTerm,
          sendKey: (s) => onInputRef.current?.(s),
          fit: handleResize,
          refit: refitNow,
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

  return (
    // No onClick handler — ghostty installs mousedown/touchend listeners on
    // its canvas that already call textarea.focus(). React's synthetic onClick
    // here would compete with ghostty's preventDefault on touchend.
    <div
      ref={containerRef}
      className="flex-1 min-h-0 bg-bg"
    />
  );
}
