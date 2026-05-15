package main

import (
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"log"
	"os"
	"os/exec"
	"strings"
	"sync"
	"time"
)

type SessionMetadata struct {
	CWD    string   `json:"cwd"`
	Branch string   `json:"branch"`
	Ports  []string `json:"ports"`
}

type Session struct {
	ID         string          `json:"id"`
	Name       string          `json:"name"`
	TmuxName   string          `json:"tmuxName"`
	Aura       bool            `json:"aura"`
	AuraReason string          `json:"auraReason"`
	CreatedAt  int64           `json:"createdAt"`
	Metadata   SessionMetadata `json:"metadata"`

	mu sync.Mutex // protects Name, Aura, AuraReason, Metadata

	// One PTY per session, owned by aurex. WebSocket clients are subscribers
	// that receive bytes from buffer; they no longer own their own tmux attach.
	runMu sync.Mutex
	pty   *os.File
	cmd   *exec.Cmd
	cols  int // last applied PTY size — used to suppress no-op resizes
	rows  int

	// Per-session ring buffer + cursor. New subscribers replay from their
	// last cursor on connect (or buffer start if first time / cursor lost).
	buffer *OutputBuffer

	subMu      sync.Mutex
	activeSubs map[*Subscriber]bool

	// lastOutputAt is set whenever the PTY emits a byte. The idle detector
	// reads it on a ticker and flips the aura on when the gap since the
	// last byte exceeds the configured silence threshold.
	lastOutputMu sync.Mutex
	lastOutputAt time.Time
}

// markOutput records that bytes were just emitted by the PTY. The idle
// detector uses this to decide whether the session is silent enough to
// be considered "agent waiting."
func (s *Session) markOutput() {
	s.lastOutputMu.Lock()
	s.lastOutputAt = time.Now()
	s.lastOutputMu.Unlock()
}

// IdleFor returns how long it's been since the last PTY output.
// Returns 0 if no output has been recorded yet.
func (s *Session) IdleFor() time.Duration {
	s.lastOutputMu.Lock()
	t := s.lastOutputAt
	s.lastOutputMu.Unlock()
	if t.IsZero() {
		return 0
	}
	return time.Since(t)
}

// Cursor returns the current end-of-stream byte cursor for this session.
func (s *Session) Cursor() int64 {
	if s.buffer == nil {
		return 0
	}
	return s.buffer.Cursor()
}

type SessionStore struct {
	mu        sync.RWMutex
	sessions  map[string]*Session
	prefix    string
	shell     string
	push      *PushManager
	hookPort  int // tmux silence hook curls http://127.0.0.1:<hookPort>/api/hook/aura
	silenceSec int // seconds of pane silence that fires the aura

	// onUpdate is called whenever a session is created, deleted, or mutated.
	onUpdate func(*Session)
}

func NewSessionStore(prefix, shell string, push *PushManager, hookPort, silenceSec int) *SessionStore {
	if hookPort <= 0 {
		hookPort = 7681
	}
	if silenceSec <= 0 {
		silenceSec = 5
	}
	return &SessionStore{
		sessions:   make(map[string]*Session),
		prefix:     prefix,
		shell:      shell,
		push:       push,
		hookPort:   hookPort,
		silenceSec: silenceSec,
	}
}

func (s *SessionStore) SetOnUpdate(fn func(*Session)) {
	s.onUpdate = fn
}

func newSessionID() string {
	var b [4]byte
	if _, err := rand.Read(b[:]); err != nil {
		// crypto/rand failing in practice means the OS is broken; fall back to time.
		return fmt.Sprintf("%x", time.Now().UnixNano())[:8]
	}
	return hex.EncodeToString(b[:])
}

// Create creates a fresh session backed by a new tmux session.
func (s *SessionStore) Create(name string) (*Session, error) {
	id := newSessionID()
	tmuxName := fmt.Sprintf("%s-%s", s.prefix, id)
	if name == "" {
		name = "session-" + id
	}
	if err := TmuxNewSession(tmuxName, s.shell); err != nil {
		return nil, err
	}
	TmuxConfigureSilenceHook(tmuxName, s.silenceSec, s.hookPort)
	sess := &Session{
		ID:         id,
		Name:       name,
		TmuxName:   tmuxName,
		CreatedAt:  time.Now().Unix(),
		buffer:     NewOutputBuffer(2 << 20), // 2 MiB ring per session
		activeSubs: make(map[*Subscriber]bool),
	}
	if err := startSession(sess, s, s.push); err != nil {
		log.Printf("aurex: start session %s: %v", tmuxName, err)
	}
	s.mu.Lock()
	s.sessions[id] = sess
	s.mu.Unlock()
	s.notifyUpdate(sess)
	return sess, nil
}

// AdoptExisting rebuilds in-memory state for tmux sessions that already exist on disk
// (e.g. survived an aurex restart). Called at startup.
func (s *SessionStore) AdoptExisting() error {
	names, err := TmuxListSessions()
	if err != nil {
		return err
	}
	prefix := s.prefix + "-"
	for _, n := range names {
		if !strings.HasPrefix(n, prefix) {
			continue
		}
		id := strings.TrimPrefix(n, prefix)
		sess := &Session{
			ID:         id,
			Name:       n,
			TmuxName:   n,
			CreatedAt:  time.Now().Unix(),
			buffer:     NewOutputBuffer(2 << 20),
			activeSubs: make(map[*Subscriber]bool),
		}
		// Idempotently enable mouse mode + disable status bar + allow OSC
		// passthrough on adopted sessions (no-op if already set).
		_ = exec.Command("tmux", "set-option", "-t", n, "mouse", "on").Run()
		_ = exec.Command("tmux", "set-option", "-t", n, "status", "off").Run()
		_ = exec.Command("tmux", "set-option", "-t", n, "allow-passthrough", "on").Run()
		TmuxConfigureSilenceHook(n, s.silenceSec, s.hookPort)
		if err := startSession(sess, s, s.push); err != nil {
			log.Printf("aurex: start adopted session %s: %v", n, err)
		}
		s.mu.Lock()
		s.sessions[id] = sess
		s.mu.Unlock()
	}
	return nil
}

// Get returns a session by ID, or nil if not found.
func (s *SessionStore) Get(id string) *Session {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.sessions[id]
}

// FindByTmuxName resolves a session given either its ID or its tmux name.
func (s *SessionStore) FindByTmuxName(nameOrID string) *Session {
	s.mu.RLock()
	defer s.mu.RUnlock()
	if sess, ok := s.sessions[nameOrID]; ok {
		return sess
	}
	for _, sess := range s.sessions {
		if sess.TmuxName == nameOrID {
			return sess
		}
	}
	return nil
}

// List returns a snapshot copy of all sessions.
func (s *SessionStore) List() []*Session {
	s.mu.RLock()
	defer s.mu.RUnlock()
	out := make([]*Session, 0, len(s.sessions))
	for _, sess := range s.sessions {
		out = append(out, sess)
	}
	return out
}

// Rename updates a session's display name. The underlying tmux session name
// (TmuxName) is left unchanged so existing attachments don't break.
func (s *SessionStore) Rename(id, name string) error {
	if name == "" {
		return fmt.Errorf("name required")
	}
	s.mu.RLock()
	sess, ok := s.sessions[id]
	s.mu.RUnlock()
	if !ok {
		return fmt.Errorf("session %q not found", id)
	}
	sess.mu.Lock()
	sess.Name = name
	sess.mu.Unlock()
	s.notifyUpdate(sess)
	return nil
}

// Delete removes the session and kills its tmux session.
func (s *SessionStore) Delete(id string) error {
	s.mu.Lock()
	sess, ok := s.sessions[id]
	if !ok {
		s.mu.Unlock()
		return fmt.Errorf("session %q not found", id)
	}
	delete(s.sessions, id)
	s.mu.Unlock()

	stopSession(sess)
	if err := TmuxKillSession(sess.TmuxName); err != nil {
		return err
	}
	s.notifyUpdate(sess)
	return nil
}

// SetAura toggles the aura flag and broadcasts the change. Returns
// (changed, edgeOn) — changed=true if anything mutated, edgeOn=true if this
// call took aura from off→on. Push-firing call sites should check edgeOn so
// they only buzz the phone on transitions, not on every TUI redraw that
// re-matches a regex.
func (s *SessionStore) SetAura(sess *Session, active bool, reason string) (changed, edgeOn bool) {
	sess.mu.Lock()
	prev := sess.Aura
	changed = sess.Aura != active || sess.AuraReason != reason
	edgeOn = active && !prev
	sess.Aura = active
	sess.AuraReason = reason
	sess.mu.Unlock()
	if changed {
		s.notifyUpdate(sess)
	}
	return
}

// ClearAura is called when the user types into a session. No-op if not glowing.
func (s *SessionStore) ClearAura(sess *Session) {
	sess.mu.Lock()
	if !sess.Aura {
		sess.mu.Unlock()
		return
	}
	sess.Aura = false
	sess.AuraReason = ""
	sess.mu.Unlock()
	s.notifyUpdate(sess)
}

func (s *SessionStore) notifyUpdate(sess *Session) {
	if s.onUpdate != nil {
		s.onUpdate(sess)
	}
}

// PollIdle watches every session's *visible* pane content and flips the aura
// on when the content hasn't changed for the configured silence threshold.
//
// We can't use raw byte-level idle because agent TUIs (claude code, codex,
// etc.) render cursor blinks and other animations every second or so — the
// PTY stream is never truly silent while a TUI is active. Hashing what tmux
// would print to the screen (via capture-pane, which excludes the live cursor
// position) is stable across cosmetic redraws and only changes when actual
// text changes — which is exactly "agent printed something new" vs "agent
// is sitting at a prompt waiting."
//
// On first observation of a session, we record its hash. When the hash stops
// changing for s.silenceSec seconds, the aura fires. The hash state is
// cleared when the aura fires (or is manually cleared) so the cycle can
// repeat after the user responds.
func (s *SessionStore) PollIdle(stop <-chan struct{}) {
	if s.silenceSec <= 0 {
		return
	}
	threshold := time.Duration(s.silenceSec) * time.Second
	t := time.NewTicker(1 * time.Second)
	defer t.Stop()

	type state struct {
		hash       uint64
		stableSince time.Time
	}
	prev := map[string]state{}

	for {
		select {
		case <-stop:
			return
		case <-t.C:
			seen := map[string]bool{}
			for _, sess := range s.List() {
				seen[sess.ID] = true
				sess.mu.Lock()
				auraOn := sess.Aura
				sess.mu.Unlock()
				if auraOn {
					delete(prev, sess.ID)
					continue
				}
				content, err := tmuxCapturePaneText(sess.TmuxName)
				if err != nil || len(content) == 0 {
					continue
				}
				h := fnv64a(content)
				st, ok := prev[sess.ID]
				if !ok || st.hash != h {
					prev[sess.ID] = state{hash: h, stableSince: time.Now()}
					continue
				}
				if time.Since(st.stableSince) < threshold {
					continue
				}
				// Stable long enough — fire the aura, clear our state so we
				// don't fire again until the screen changes and goes idle
				// again.
				delete(prev, sess.ID)
				_, edgeOn := s.SetAura(sess, true, "Agent idle — likely waiting for input")
				if !edgeOn {
					continue // already on; don't re-push
				}
				suppressed := desktopForegroundActive(sess)
				log.Printf("idle: %s went idle (push %s)", sess.TmuxName,
					ternary(suppressed, "suppressed: laptop foreground", "firing"))
				if s.push != nil && !suppressed {
					res := s.push.Notify(NotificationPayload{
						Title:     sess.Name,
						Body:      "Agent idle — likely waiting for input",
						SessionID: sess.ID,
						Tag:       "aurex-" + sess.ID,
					})
					log.Printf("idle: %s push → total=%d sent=%d failed=%d lastErr=%q",
						sess.TmuxName, res.Total, res.Sent, res.Failed, res.LastErr)
				}
			}
			// Drop state for sessions that no longer exist.
			for id := range prev {
				if !seen[id] {
					delete(prev, id)
				}
			}
		}
	}
}

// tmuxCapturePaneText returns the rendered text of the session's active pane
// (no escape codes). Used by the idle detector to hash the logical screen
// state, ignoring cursor-blink and similar cosmetic byte churn.
func tmuxCapturePaneText(name string) ([]byte, error) {
	out, err := exec.Command("tmux", "capture-pane", "-p", "-t", name).Output()
	if err != nil {
		return nil, err
	}
	return out, nil
}

// ternary is a tiny string conditional for log lines.
func ternary(cond bool, a, b string) string {
	if cond {
		return a
	}
	return b
}

// fnv64a is the FNV-1a 64-bit hash. Non-cryptographic but plenty for change
// detection. Inlined to keep the dependency surface small.
func fnv64a(data []byte) uint64 {
	const offset64 = 14695981039346656037
	const prime64 = 1099511628211
	var h uint64 = offset64
	for _, b := range data {
		h ^= uint64(b)
		h *= prime64
	}
	return h
}

// PollMetadata refreshes CWD + git branch for every session every interval.
// Sends an update for any session whose metadata changed.
func (s *SessionStore) PollMetadata(interval time.Duration, stop <-chan struct{}) {
	t := time.NewTicker(interval)
	defer t.Stop()
	for {
		select {
		case <-stop:
			return
		case <-t.C:
			for _, sess := range s.List() {
				cwd, err := TmuxPaneCWD(sess.TmuxName)
				if err != nil {
					continue
				}
				branch := gitBranch(cwd)
				sess.mu.Lock()
				changed := sess.Metadata.CWD != cwd || sess.Metadata.Branch != branch
				sess.Metadata.CWD = cwd
				sess.Metadata.Branch = branch
				sess.mu.Unlock()
				if changed {
					s.notifyUpdate(sess)
				}
			}
		}
	}
}

func gitBranch(cwd string) string {
	if cwd == "" {
		return ""
	}
	cmd := exec.Command("git", "-C", cwd, "branch", "--show-current")
	out, err := cmd.Output()
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(out))
}
