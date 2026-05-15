import { useState } from 'react';

function SessionRow({ session, active, onSelect, onRename, onDelete }) {
  const branch = session.metadata?.branch;
  const cwd = session.metadata?.cwd;
  const [editing, setEditing] = useState(false);
  const [draft, setDraft] = useState(session.name);

  const commitRename = () => {
    const trimmed = draft.trim();
    setEditing(false);
    if (trimmed && trimmed !== session.name) onRename(session.id, trimmed);
    else setDraft(session.name);
  };

  return (
    <div
      onClick={() => !editing && onSelect(session.id)}
      className={[
        'group relative cursor-pointer rounded-lg border bg-panel px-3 py-3 transition-colors',
        active ? 'border-line bg-aura/[0.06] ring-1 ring-inset ring-aura/30' : 'border-line hover:border-zinc-600',
        active ? 'border-l-4 border-l-aura' : 'border-l-4 border-l-transparent',
        session.aura ? 'animate-aura' : '',
      ].join(' ')}
    >
      <div className="flex items-center justify-between gap-2">
        <div className="min-w-0 flex-1">
          {editing ? (
            <input
              autoFocus
              value={draft}
              onChange={(e) => setDraft(e.target.value)}
              onClick={(e) => e.stopPropagation()}
              onBlur={commitRename}
              onKeyDown={(e) => {
                if (e.key === 'Enter') commitRename();
                if (e.key === 'Escape') {
                  setDraft(session.name);
                  setEditing(false);
                }
              }}
              className="w-full rounded border border-aura/40 bg-bg px-2 py-1 text-base text-zinc-100 focus:outline-none"
            />
          ) : (
            <div className="truncate text-base font-medium text-zinc-100">{session.name}</div>
          )}
          <div className="mt-1 flex flex-wrap items-center gap-x-3 gap-y-0.5 text-xs text-zinc-400">
            {branch && (
              <span className="truncate">
                <span className="text-zinc-500">⎇</span> {branch}
              </span>
            )}
            {cwd && (
              <span className="truncate font-mono">
                {cwd.replace(/^\/home\/[^/]+/, '~')}
              </span>
            )}
          </div>
          {session.aura && session.auraReason && (
            <div className="mt-1.5 line-clamp-2 text-xs text-aura">
              {session.auraReason}
            </div>
          )}
        </div>
        <div className="flex shrink-0 items-center">
          <button
            tabIndex={-1}
            onClick={(e) => {
              e.stopPropagation();
              setDraft(session.name);
              setEditing(true);
            }}
            onMouseDown={(e) => e.preventDefault()}
            onTouchStart={(e) => e.preventDefault()}
            className="rounded p-2 text-zinc-500 hover:bg-zinc-800 hover:text-aura"
            aria-label="Rename session"
            title="Rename"
          >
            ✎
          </button>
          <button
            tabIndex={-1}
            onClick={(e) => {
              e.stopPropagation();
              if (confirm(`Kill session "${session.name}"?`)) onDelete(session.id);
            }}
            onMouseDown={(e) => e.preventDefault()}
            onTouchStart={(e) => e.preventDefault()}
            className="rounded p-2 text-zinc-500 hover:bg-zinc-800 hover:text-red-400 active:bg-zinc-800"
            aria-label="Delete session"
          >
            ✕
          </button>
        </div>
      </div>
    </div>
  );
}

export default function Sidebar({
  sessions,
  activeId,
  onSelect,
  onCreate,
  onRename,
  onDelete,
  pushSubscribed,
  pushIsSecure,
  connected,
  onOpenPush,
  open,
  onClose,
}) {
  const [creating, setCreating] = useState(false);
  const [newName, setNewName] = useState('');

  const handleCreate = async () => {
    setCreating(true);
    try {
      await onCreate(newName.trim());
      setNewName('');
    } finally {
      setCreating(false);
    }
  };

  return (
    <>
      {open && (
        <div
          className="fixed inset-0 z-30 bg-black/60 md:hidden"
          onClick={onClose}
          aria-hidden="true"
        />
      )}
      <aside
        className={[
          'z-40 flex flex-col gap-3 border-r border-line bg-panel p-3',
          'fixed inset-y-0 left-0 w-80 transform transition-transform md:relative md:translate-x-0 md:w-72',
          open ? 'translate-x-0' : '-translate-x-full',
        ].join(' ')}
        style={{
          paddingTop: 'max(0.75rem, env(safe-area-inset-top))',
          paddingBottom: 'max(0.75rem, env(safe-area-inset-bottom))',
        }}
      >
        <div className="flex items-center justify-between">
          <div className="text-lg font-semibold tracking-wide">
            <span className="text-aura">aurex</span>
          </div>
          <button
            onClick={onClose}
            onMouseDown={(e) => e.preventDefault()}
            onTouchStart={(e) => e.preventDefault()}
            className="rounded p-1 text-zinc-400 hover:bg-zinc-800 md:hidden"
            aria-label="Close sidebar"
          >
            ✕
          </button>
        </div>

        <div className="flex gap-2">
          <input
            value={newName}
            onChange={(e) => setNewName(e.target.value)}
            onKeyDown={(e) => e.key === 'Enter' && handleCreate()}
            placeholder="new session name"
            className="min-w-0 flex-1 rounded-md border border-line bg-bg px-3 py-2 text-sm placeholder-zinc-500 focus:border-aura focus:outline-none"
          />
          <button
            onClick={handleCreate}
            disabled={creating}
            className="rounded-md border border-aura/40 bg-aura/10 px-4 py-2 text-sm font-medium text-aura hover:bg-aura/20 disabled:opacity-50"
            aria-label="Create session"
          >
            +
          </button>
        </div>

        <div className="flex-1 space-y-2 overflow-y-auto pr-1">
          {sessions.length === 0 && (
            <div className="rounded border border-dashed border-line p-4 text-center text-xs text-zinc-500">
              No sessions yet.
              <br />
              <span className="text-zinc-400">Type a name and tap +</span>
            </div>
          )}
          {sessions.map((s) => (
            <SessionRow
              key={s.id}
              session={s}
              active={s.id === activeId}
              onSelect={(id) => {
                onSelect(id);
                onClose?.();
              }}
              onRename={onRename}
              onDelete={onDelete}
            />
          ))}
        </div>

        {/* Footer: push status + connection dot.
            On mobile the same info is in the header; this is also the desktop
            display since the desktop header is hidden. */}
        <div className="flex items-center gap-2 border-t border-line pt-3">
          <button
            onClick={onOpenPush}
            onMouseDown={(e) => e.preventDefault()}
            onTouchStart={(e) => e.preventDefault()}
            className={[
              'flex-1 rounded-md border px-3 py-2 text-xs',
              pushSubscribed
                ? 'border-emerald-500/40 bg-emerald-500/10 text-emerald-300'
                : pushIsSecure
                ? 'border-line bg-bg text-zinc-300 hover:border-aura/40 hover:text-aura'
                : 'border-amber-500/40 bg-amber-500/10 text-amber-300',
            ].join(' ')}
          >
            {pushSubscribed
              ? '🔔 Notifications on'
              : pushIsSecure
              ? 'Notifications: tap to set up'
              : 'Push blocked — tap for help'}
          </button>
          <span
            className={[
              'inline-block h-2 w-2 rounded-full',
              connected ? 'bg-emerald-400' : 'bg-zinc-600',
            ].join(' ')}
            title={connected ? 'connected' : 'disconnected'}
          />
        </div>
      </aside>
    </>
  );
}
