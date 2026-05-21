import { useCallback, useEffect, useMemo, useRef, useState } from 'react';
import Sidebar from './components/Sidebar.jsx';
import Terminal from './components/Terminal.jsx';
import Toolbar, { ctrlOf } from './components/Toolbar.jsx';
import PushPanel from './components/PushPanel.jsx';
import TranscriptPanel from './components/TranscriptPanel.jsx';
import { useSession } from './hooks/useSession.js';
import { usePush } from './hooks/usePush.js';

function getInitialSessionId() {
  const params = new URLSearchParams(window.location.search);
  return params.get('session') || null;
}

function setUrlSession(id) {
  const url = new URL(window.location.href);
  if (id) url.searchParams.set('session', id);
  else url.searchParams.delete('session');
  window.history.replaceState({}, '', url);
}

// useKeyboardInset publishes the height occluded by the soft keyboard as a
// CSS variable (--kbd-inset). On iOS the layout viewport doesn't shrink when
// the keyboard opens, so we shrink it ourselves via visualViewport.
//
// IMPORTANT: do not subscribe to vv `scroll` events. iOS Safari fires them on
// every keystroke (auto-scrolling the focused input into view), and the
// resulting layout thrash makes typing feel laggy/buggy.
function useKeyboardInset() {
  useEffect(() => {
    const vv = window.visualViewport;
    if (!vv) return undefined;
    const root = document.documentElement;
    let last = -1;
    const update = () => {
      const raw = window.innerHeight - vv.height - vv.offsetTop;
      const inset = Math.max(0, Math.round(raw));
      if (inset === last) return;
      last = inset;
      root.style.setProperty('--kbd-inset', `${inset}px`);
    };
    update();
    vv.addEventListener('resize', update);
    return () => {
      vv.removeEventListener('resize', update);
      root.style.removeProperty('--kbd-inset');
    };
  }, []);
}

export default function App() {
  const [sessions, setSessions] = useState([]);
  const [activeId, setActiveId] = useState(getInitialSessionId());
  const [sidebarOpen, setSidebarOpen] = useState(false);
  const [pushPanelOpen, setPushPanelOpen] = useState(false);
  const [transcriptOpen, setTranscriptOpen] = useState(false);
  const [ctrlArmed, setCtrlArmed] = useState(false);
  const termHandleRef = useRef(null);

  useKeyboardInset();
  const push = usePush();
  const activeSession = useMemo(
    () => sessions.find((s) => s.id === activeId) || null,
    [sessions, activeId]
  );

  const refreshSessions = useCallback(async () => {
    const res = await fetch('/api/sessions');
    if (!res.ok) return;
    const data = await res.json();
    setSessions(data.sessions || []);
  }, []);

  useEffect(() => {
    refreshSessions();
  }, [refreshSessions]);

  useEffect(() => {
    setUrlSession(activeId);
  }, [activeId]);

  // If we have sessions but no active one, default to the first.
  useEffect(() => {
    if (!activeId && sessions.length > 0) setActiveId(sessions[0].id);
  }, [activeId, sessions]);

  const handleTerminalReady = useCallback((handle) => {
    termHandleRef.current = handle;
  }, []);

  const handleData = useCallback((data) => {
    termHandleRef.current?.write(data);
  }, []);

  const handleSessionUpdate = useCallback((updated) => {
    setSessions((cur) => {
      const idx = cur.findIndex((s) => s.id === updated.id);
      if (idx === -1) return [...cur, updated];
      const next = cur.slice();
      next[idx] = updated;
      return next;
    });
  }, []);

  const handleSessionsList = useCallback((list) => {
    setSessions(list || []);
  }, []);

  const handleWSOpen = useCallback(() => {
    // Force a fit AND an unconditional resize-message send. ghostty's onResize
    // only fires when its internal dimensions change, so if another device
    // shrank the server's PTY, switching back to a bigger device wouldn't
    // emit a resize on its own — refit() guarantees we send our current size.
    termHandleRef.current?.refit?.();
  }, []);

  const { sendInput, sendResize, connected } = useSession({
    sessionId: activeId,
    onData: handleData,
    onSessionUpdate: handleSessionUpdate,
    onSessionsList: handleSessionsList,
    onOpen: handleWSOpen,
  });

  const handleTermInput = useCallback(
    (data) => {
      if (ctrlArmed) {
        const ctrl = ctrlOf(data);
        if (ctrl) {
          sendInput(ctrl);
          setCtrlArmed(false);
          return;
        }
        // Non-letter: drop the arm and send normally.
        setCtrlArmed(false);
      }
      sendInput(data);
    },
    [ctrlArmed, sendInput]
  );

  const handleResize = useCallback(
    (cols, rows) => sendResize(cols, rows),
    [sendResize]
  );

  const handleCreate = useCallback(
    async (name) => {
      const res = await fetch('/api/sessions', {
        method: 'POST',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify({ name: name || '' }),
      });
      if (!res.ok) return;
      const sess = await res.json();
      setSessions((cur) => [...cur, sess]);
      setActiveId(sess.id);
    },
    []
  );

  const handleDelete = useCallback(
    async (id) => {
      await fetch(`/api/sessions/${id}`, { method: 'DELETE' });
      setSessions((cur) => cur.filter((s) => s.id !== id));
      if (activeId === id) setActiveId(null);
    },
    [activeId]
  );

  const handleRename = useCallback(async (id, name) => {
    const res = await fetch(`/api/sessions/${id}`, {
      method: 'PATCH',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify({ name }),
    });
    if (res.ok) {
      const updated = await res.json();
      setSessions((cur) => cur.map((s) => (s.id === id ? updated : s)));
    }
  }, []);

  const handleToolbarKey = useCallback(
    (seq) => {
      sendInput(seq);
      termHandleRef.current?.focus();
    },
    [sendInput]
  );

  return (
    <div className="flex h-full w-full overflow-hidden bg-bg text-zinc-100">
      <Sidebar
        sessions={sessions}
        activeId={activeId}
        onSelect={setActiveId}
        onCreate={handleCreate}
        onRename={handleRename}
        onDelete={handleDelete}
        pushSubscribed={push.subscribed}
        pushIsSecure={push.isSecure}
        connected={connected}
        onOpenPush={() => setPushPanelOpen(true)}
        open={sidebarOpen}
        onClose={() => setSidebarOpen(false)}
      />

      <main className="flex min-w-0 flex-1 flex-col">
        {/* Mobile-only header. Desktop relies on the sidebar for the same info. */}
        <header className="flex shrink-0 items-center justify-between border-b border-line bg-panel px-3 py-2 md:hidden">
          <button
            onClick={() => setSidebarOpen(true)}
            onMouseDown={(e) => e.preventDefault()}
            onTouchStart={(e) => e.preventDefault()}
            className="rounded p-1 text-zinc-300 hover:bg-zinc-800"
            aria-label="Open sessions"
          >
            ☰
          </button>
          <div className="min-w-0 flex-1 px-2">
            <div className="truncate text-sm font-medium">
              {activeSession ? activeSession.name : 'No session selected'}
            </div>
          </div>
          <div className="flex items-center gap-2 text-xs">
            <button
              onClick={() => setTranscriptOpen(true)}
              onMouseDown={(e) => e.preventDefault()}
              onTouchStart={(e) => e.preventDefault()}
              disabled={!activeSession}
              className="rounded-md border border-line bg-bg px-2 py-1 text-[11px] text-zinc-300 disabled:opacity-40"
              title="Full text transcript (includes content lost to TUI redraws)"
            >
              transcript
            </button>
            <button
              onClick={() => setPushPanelOpen(true)}
              onMouseDown={(e) => e.preventDefault()}
              onTouchStart={(e) => e.preventDefault()}
              className={[
                'rounded-md border px-2 py-1 text-[11px]',
                push.subscribed
                  ? 'border-emerald-500/40 bg-emerald-500/10 text-emerald-300'
                  : push.isSecure
                  ? 'border-line bg-bg text-zinc-300'
                  : 'border-amber-500/40 bg-amber-500/10 text-amber-300',
              ].join(' ')}
              title="Push status"
            >
              push: {push.subscribed ? 'on' : push.isSecure ? 'off' : 'blocked'}
            </button>
            <span
              className={[
                'inline-block h-2 w-2 rounded-full',
                connected ? 'bg-emerald-400' : 'bg-zinc-600',
              ].join(' ')}
              title={connected ? 'connected' : 'disconnected'}
            />
          </div>
        </header>

        {activeSession ? (
          <>
            <Terminal
              key={activeSession.id}
              onReady={handleTerminalReady}
              onInput={handleTermInput}
              onResize={handleResize}
            />
            {/* Mobile-only toolbar — desktop keyboards already have these keys. */}
            <div className="md:hidden">
              <Toolbar
                onSendKey={handleToolbarKey}
                ctrlArmed={ctrlArmed}
                onCtrlArm={() => setCtrlArmed((v) => !v)}
              />
            </div>
          </>
        ) : (
          <div className="flex flex-1 items-center justify-center text-center text-sm text-zinc-500">
            <div>
              <div className="mb-2 text-lg text-zinc-300">welcome to aurex</div>
              <div>Create or pick a session from the sidebar.</div>
            </div>
          </div>
        )}
      </main>

      {pushPanelOpen && <PushPanel push={push} onClose={() => setPushPanelOpen(false)} />}
      {transcriptOpen && activeSession && (
        <TranscriptPanel
          sessionId={activeSession.id}
          sessionName={activeSession.name}
          onClose={() => setTranscriptOpen(false)}
        />
      )}
    </div>
  );
}
