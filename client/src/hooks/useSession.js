import { useCallback, useEffect, useRef, useState } from 'react';

const cursorKey = (id) => `aurex:cursor:${id}`;

function readCursor(id) {
  try {
    const raw = window.localStorage.getItem(cursorKey(id));
    if (!raw) return 0;
    const n = Number(raw);
    return Number.isFinite(n) && n >= 0 ? n : 0;
  } catch {
    return 0;
  }
}

function writeCursor(id, cursor) {
  try {
    window.localStorage.setItem(cursorKey(id), String(cursor));
  } catch {}
}

/**
 * useSession owns a WebSocket connection to a single aurex session.
 *
 * Cursor-based resume: the hook remembers the last byte cursor delivered by
 * the server in localStorage and sends it as ?cursor=N on every (re)connect.
 * The server replays any missed bytes from its ring buffer before streaming
 * live output, so refresh / device-switch / drop-and-reconnect don't lose
 * any output and naturally rehydrate xterm's scrollback.
 *
 * Callbacks:
 *   - onData(string): called with each "output" frame's data
 *   - onSessionUpdate(session): server pushed a metadata change
 *   - onSessionsList(sessions): full list refresh (e.g. someone else created)
 *   - onOpen(): WS just opened, useful for forcing xterm to refit
 */
export function useSession({ sessionId, onData, onSessionUpdate, onSessionsList, onOpen }) {
  const wsRef = useRef(null);
  const [connected, setConnected] = useState(false);
  const reconnectTimerRef = useRef(null);

  const handlersRef = useRef({ onData, onSessionUpdate, onSessionsList, onOpen });
  useEffect(() => {
    handlersRef.current = { onData, onSessionUpdate, onSessionsList, onOpen };
  }, [onData, onSessionUpdate, onSessionsList, onOpen]);

  useEffect(() => {
    if (!sessionId) return undefined;

    let cancelled = false;

    const connect = () => {
      if (cancelled) return;
      const proto = window.location.protocol === 'https:' ? 'wss:' : 'ws:';
      const cursor = readCursor(sessionId);
      // Rough size estimate so the server can resize the PTY BEFORE capturing
      // any output. Without this, the server's snapshot is taken at whatever
      // size the previous client left the PTY at, then painted into our
      // (different-sized) terminal — garbled wraps from the old size leak
      // through as narrow strips on the left while the new content paints
      // correctly. ghostty's actual fit() sends a corrective resize moments
      // later if our estimate is off.
      const desktop = window.matchMedia('(min-width: 768px)').matches;
      const fontSize = desktop ? 15 : 14;
      const cellW = fontSize * 0.6;
      const cellH = fontSize * 1.2;
      const cols = Math.max(20, Math.floor(window.innerWidth / cellW));
      const rows = Math.max(5, Math.floor(window.innerHeight / cellH));
      const ws = new WebSocket(
        `${proto}//${window.location.host}/ws/${sessionId}?cursor=${cursor}&cols=${cols}&rows=${rows}`,
      );
      wsRef.current = ws;

      ws.onopen = () => {
        setConnected(true);
        handlersRef.current.onOpen?.();
      };
      ws.onclose = () => {
        setConnected(false);
        wsRef.current = null;
        if (cancelled) return;
        reconnectTimerRef.current = setTimeout(connect, 1000);
      };
      ws.onerror = () => {
        // onclose fires after onerror; let it handle reconnect.
      };
      ws.onmessage = (event) => {
        // Belt-and-suspenders: even after ws.close() + null'd handlers, already-
        // queued message events can still fire. Drop them here so session A's
        // late bytes can't paint into session B's ghostty.
        if (cancelled) return;
        let msg;
        try {
          msg = JSON.parse(event.data);
        } catch {
          return;
        }
        const h = handlersRef.current;
        if (msg.type === 'output') {
          if (h.onData) h.onData(msg.data);
          if (typeof msg.cursor === 'number') writeCursor(sessionId, msg.cursor);
        } else if (msg.type === 'session_update' && h.onSessionUpdate) {
          h.onSessionUpdate(msg.session);
        } else if (msg.type === 'sessions_list' && h.onSessionsList) {
          h.onSessionsList(msg.sessions);
        }
      };
    };

    connect();

    return () => {
      cancelled = true;
      if (reconnectTimerRef.current) clearTimeout(reconnectTimerRef.current);
      if (wsRef.current) {
        // Null out ALL handlers — close() is async, so onmessage events for
        // bytes already in the receive buffer can still fire after close().
        // If we don't suppress them they cross into the NEW session's ghostty
        // (because handleData writes to the live termHandleRef), producing
        // the "old session showing through new session" overlap bug.
        wsRef.current.onmessage = null;
        wsRef.current.onopen = null;
        wsRef.current.onclose = null;
        wsRef.current.onerror = null;
        wsRef.current.close();
        wsRef.current = null;
      }
    };
  }, [sessionId]);

  const sendInput = useCallback((data) => {
    const ws = wsRef.current;
    if (!ws || ws.readyState !== WebSocket.OPEN) return;
    ws.send(JSON.stringify({ type: 'input', data }));
  }, []);

  const sendResize = useCallback((cols, rows) => {
    const ws = wsRef.current;
    if (!ws || ws.readyState !== WebSocket.OPEN) return;
    ws.send(JSON.stringify({ type: 'resize', cols, rows }));
  }, []);

  return { connected, sendInput, sendResize };
}
