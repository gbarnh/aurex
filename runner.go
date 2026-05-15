package main

import (
	"encoding/json"
	"io"
	"log"
	"os"
	"os/exec"
	"regexp"
	"strings"
	"time"

	"github.com/acarl005/stripansi"
	"github.com/creack/pty"
	"github.com/gorilla/websocket"
)

// auraPatterns detect agent prompts in PTY output. The detector strips ANSI
// before matching to keep the regexes readable.
var auraPatterns = []*regexp.Regexp{
	regexp.MustCompile(`(?i)do you want to proceed\?`),
	regexp.MustCompile(`(?i)\(y/n\)`),
	regexp.MustCompile(`(?i)press enter to continue`),
	regexp.MustCompile(`(?i)claude is waiting`),
	regexp.MustCompile(`(?i)allow this action\?`),
	regexp.MustCompile(`(?i)\[yes/no\]`),
	regexp.MustCompile(`(?i)continue\?`),
	regexp.MustCompile(`(?i)shall i`),
	regexp.MustCompile(`(?i)waiting for input`),
}

// SubscriberMessage is what gets sent to each WebSocket subscriber for an
// output frame. cursor is the end-of-stream position after this data — the
// client persists it so it can ?cursor=N on reconnect.
//
// rawPayload, when non-nil, is sent verbatim (a pre-marshaled JSON object).
// This lets the metadata broadcaster reuse one marshal call for many
// subscribers without funneling through this struct's JSON shape.
type SubscriberMessage struct {
	Type   string `json:"type"`
	Data   string `json:"data,omitempty"`
	Cursor int64  `json:"cursor,omitempty"`

	rawPayload []byte `json:"-"`
}

// Subscriber is one WebSocket attached to a session. Writes go through a
// channel so multiple producers (broadcast goroutine, replay) don't race on
// the same conn.
type Subscriber struct {
	conn *websocket.Conn
	send chan SubscriberMessage
	stop chan struct{}
}

func newSubscriber(conn *websocket.Conn) *Subscriber {
	return &Subscriber{
		conn: conn,
		// Buffered so the broadcaster can keep moving even if one client is slow.
		// 64 frames ≈ a few seconds of typical output; beyond that the slow
		// client gets dropped (writer loop exits, ws gets unsubscribed).
		send: make(chan SubscriberMessage, 64),
		stop: make(chan struct{}),
	}
}

func (s *Subscriber) writer() {
	defer s.conn.Close()
	for {
		select {
		case msg, ok := <-s.send:
			if !ok {
				return
			}
			if msg.rawPayload != nil {
				if err := s.conn.WriteMessage(websocket.TextMessage, msg.rawPayload); err != nil {
					return
				}
				continue
			}
			if err := s.conn.WriteJSON(msg); err != nil {
				return
			}
		case <-s.stop:
			return
		}
	}
}

// Push enqueues a message for this subscriber. Drops the connection if its
// channel is full (slow consumer).
func (s *Subscriber) Push(msg SubscriberMessage) bool {
	select {
	case s.send <- msg:
		return true
	default:
		return false
	}
}

// startSession spawns the one tmux attach PTY for sess, captures all bytes
// into the ring buffer, and broadcasts to subscribers. Idempotent — no-op if
// the session already has a running PTY.
func startSession(sess *Session, store *SessionStore, push *PushManager) error {
	sess.runMu.Lock()
	if sess.pty != nil {
		sess.runMu.Unlock()
		return nil
	}
	cmd := exec.Command("tmux", "attach-session", "-d", "-t", sess.TmuxName)
	cmd.Env = append(os.Environ(), "TERM=xterm-256color")
	ptmx, err := pty.Start(cmd)
	if err != nil {
		sess.runMu.Unlock()
		return err
	}
	sess.pty = ptmx
	sess.cmd = cmd
	sess.runMu.Unlock()

	go runCapture(sess, store, push)
	return nil
}

func runCapture(sess *Session, store *SessionStore, push *PushManager) {
	var auraTail strings.Builder
	buf := make([]byte, 8192)
	for {
		n, err := sess.pty.Read(buf)
		if n > 0 {
			data := make([]byte, n)
			copy(data, buf[:n])
			cursor := sess.buffer.Append(data)
			broadcast(sess, SubscriberMessage{
				Type:   "output",
				Data:   string(data),
				Cursor: cursor,
			})
			detectAura(sess, store, push, &auraTail, data)
		}
		if err != nil {
			if err != io.EOF {
				log.Printf("session[%s] capture: %v — attempting reattach", sess.TmuxName, err)
			}
			sess.runMu.Lock()
			if sess.pty != nil {
				_ = sess.pty.Close()
			}
			sess.pty = nil
			sess.cmd = nil
			sess.runMu.Unlock()
			// Auto-reattach if the tmux session is still alive. tmux can detach
			// our attach for reasons outside our control (server reload,
			// resize race, another client with -d). Reattaching transparently
			// keeps the experience seamless from the client side.
			if TmuxHasSession(sess.TmuxName) {
				time.Sleep(200 * time.Millisecond)
				if err := startSession(sess, store, push); err != nil {
					log.Printf("session[%s] reattach failed: %v", sess.TmuxName, err)
				}
			} else {
				log.Printf("session[%s] tmux session gone, capture ended", sess.TmuxName)
			}
			return
		}
	}
}

func detectAura(sess *Session, store *SessionStore, push *PushManager, tail *strings.Builder, data []byte) {
	clean := stripansi.Strip(string(data))
	if clean == "" {
		return
	}
	tail.WriteString(clean)
	if tail.Len() > 512 {
		s := tail.String()
		tail.Reset()
		tail.WriteString(s[len(s)-512:])
	}
	t := tail.String()
	for _, pat := range auraPatterns {
		if pat.MatchString(t) {
			reason := lastLine(t, 100)
			store.SetAura(sess, true, reason)
			if push != nil {
				push.Notify(NotificationPayload{
					Title:     sess.Name,
					Body:      reason,
					SessionID: sess.ID,
					Tag:       "aurex-" + sess.ID,
				})
			}
			tail.Reset()
			return
		}
	}
}

func lastLine(s string, max int) string {
	s = strings.TrimRight(s, " \t\r\n")
	if idx := strings.LastIndexAny(s, "\n\r"); idx >= 0 {
		s = s[idx+1:]
	}
	s = strings.TrimSpace(s)
	if len(s) > max {
		s = s[len(s)-max:]
	}
	return s
}

// broadcast pushes a message to every current subscriber of sess. Slow
// subscribers (full channel) get dropped — their writer goroutine exits and
// the session removes them.
func broadcast(sess *Session, msg SubscriberMessage) {
	sess.subMu.Lock()
	dead := []*Subscriber(nil)
	for sub := range sess.activeSubs {
		if !sub.Push(msg) {
			dead = append(dead, sub)
		}
	}
	for _, d := range dead {
		delete(sess.activeSubs, d)
		close(d.stop)
	}
	sess.subMu.Unlock()
}

// stopSession kills the PTY and closes all subscribers. Used on session
// delete or graceful shutdown.
func stopSession(sess *Session) {
	sess.runMu.Lock()
	if sess.pty != nil {
		_ = sess.pty.Close()
	}
	if sess.cmd != nil && sess.cmd.Process != nil {
		_ = sess.cmd.Process.Kill()
	}
	sess.pty = nil
	sess.cmd = nil
	sess.runMu.Unlock()

	sess.subMu.Lock()
	for sub := range sess.activeSubs {
		close(sub.stop)
	}
	sess.activeSubs = map[*Subscriber]bool{}
	sess.subMu.Unlock()
}

// inputMessage and resizeMessage are the schema for messages read off WS.
type clientMessage struct {
	Type string          `json:"type"`
	Data json.RawMessage `json:"data,omitempty"`
	Cols int             `json:"cols,omitempty"`
	Rows int             `json:"rows,omitempty"`
}

// WriteInput sends bytes to the session's PTY. Clearing aura on any input
// preserves the "user typed → aura goes away" behavior.
func WriteInput(sess *Session, store *SessionStore, s string) {
	sess.runMu.Lock()
	p := sess.pty
	sess.runMu.Unlock()
	if p == nil {
		return
	}
	_, _ = p.Write([]byte(s))
	store.ClearAura(sess)
}

// ResizePTY adjusts the PTY window size when it actually changes.
//
// Only an actual size change clears the ring buffer (bytes captured at the
// old size would garble at the new size). No-op resizes are common — every
// fresh client emits its size on connect via refit(), and most reconnects
// match the existing size — so we skip the clear in that case to preserve
// replay for ordinary reconnects.
func ResizePTY(sess *Session, cols, rows int) {
	if cols <= 0 || rows <= 0 {
		return
	}
	sess.runMu.Lock()
	p := sess.pty
	sameSize := sess.cols == cols && sess.rows == rows
	sess.cols = cols
	sess.rows = rows
	sess.runMu.Unlock()
	if p == nil || sameSize {
		return
	}
	if sess.buffer != nil {
		sess.buffer.Clear()
	}
	_ = pty.Setsize(p, &pty.Winsize{Cols: uint16(cols), Rows: uint16(rows)})
}
