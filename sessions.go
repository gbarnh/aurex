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
}

// Cursor returns the current end-of-stream byte cursor for this session.
func (s *Session) Cursor() int64 {
	if s.buffer == nil {
		return 0
	}
	return s.buffer.Cursor()
}

type SessionStore struct {
	mu       sync.RWMutex
	sessions map[string]*Session
	prefix   string
	shell    string
	push     *PushManager

	// onUpdate is called whenever a session is created, deleted, or mutated.
	onUpdate func(*Session)
}

func NewSessionStore(prefix, shell string, push *PushManager) *SessionStore {
	return &SessionStore{
		sessions: make(map[string]*Session),
		prefix:   prefix,
		shell:    shell,
		push:     push,
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

// SetAura toggles the aura flag and broadcasts the change.
func (s *SessionStore) SetAura(sess *Session, active bool, reason string) {
	sess.mu.Lock()
	changed := sess.Aura != active || sess.AuraReason != reason
	sess.Aura = active
	sess.AuraReason = reason
	sess.mu.Unlock()
	if changed {
		s.notifyUpdate(sess)
	}
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
