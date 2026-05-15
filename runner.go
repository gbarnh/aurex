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

// SubscriberClass distinguishes phone-like clients from laptop/desktop-like
// ones. Used by push-suppression logic in the hook handler — when a laptop
// has aurex's tab in the foreground, we don't fire a phone push.
type SubscriberClass int

const (
	ClassDesktop SubscriberClass = iota
	ClassPhone
)

// Subscriber is one WebSocket attached to a session. Writes go through a
// channel so multiple producers (broadcast goroutine, replay) don't race on
// the same conn.
type Subscriber struct {
	conn    *websocket.Conn
	send    chan SubscriberMessage
	stop    chan struct{}
	class   SubscriberClass
	visible bool // last-known browser tab visibility — drives push suppression
}

// IsForegroundDesktop reports whether this subscriber is a laptop/desktop
// client with aurex currently in the foreground (visible).
func (s *Subscriber) IsForegroundDesktop() bool {
	return s.class == ClassDesktop && s.visible
}

func newSubscriber(conn *websocket.Conn, class SubscriberClass) *Subscriber {
	return &Subscriber{
		conn:    conn,
		class:   class,
		visible: true, // assume visible until the client tells us otherwise
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
	var oscTail []byte // carry partial OSC sequences across read boundaries
	buf := make([]byte, 8192)
	for {
		n, err := sess.pty.Read(buf)
		if n > 0 {
			data := make([]byte, n)
			copy(data, buf[:n])
			cursor := sess.buffer.Append(data)
			sess.markOutput()
			broadcast(sess, SubscriberMessage{
				Type:   "output",
				Data:   string(data),
				Cursor: cursor,
			})
			oscTail = detectOSCNotifications(sess, store, push, append(oscTail, data...))
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

// detectOSCNotifications scans for terminal-native notification escape
// sequences and fires the aura when one lands. This is the same mechanism
// cmux uses — OSC 9, 99, and 777 are standard ways for processes to ask the
// terminal to notify the user.
//
//   OSC 9 (iTerm2):       ESC ] 9 ; <message> BEL
//   OSC 99 (Konsole):     ESC ] 99 ; <message> BEL
//   OSC 777 (urxvt/rxvt): ESC ] 777 ; notify ; <title> ; <message> BEL
//
// BEL = 0x07, or ST terminator (ESC \) is also valid. Sequences can span
// multiple PTY reads, so we keep a tail of unterminated bytes between calls.
//
// Returns the unterminated tail to carry into the next call (capped at 4 KiB
// so a malformed/unending sequence doesn't grow without bound).
func detectOSCNotifications(sess *Session, store *SessionStore, push *PushManager, data []byte) []byte {
	const escape byte = 0x1b
	const bel byte = 0x07
	const maxTail = 4096

	out := data
	for {
		// Find the next OSC introducer (ESC ]).
		i := -1
		for j := 0; j+1 < len(out); j++ {
			if out[j] == escape && out[j+1] == ']' {
				i = j
				break
			}
		}
		if i < 0 {
			// No OSC start in remaining buffer — discard everything since
			// nothing partial could become a complete OSC later.
			return nil
		}

		// Find a terminator (BEL or ST = ESC \) after i+2.
		end := -1
		for j := i + 2; j < len(out); j++ {
			if out[j] == bel {
				end = j
				break
			}
			if out[j] == escape && j+1 < len(out) && out[j+1] == '\\' {
				end = j + 1
				break
			}
		}
		if end < 0 {
			// Incomplete OSC — keep as tail for next call. Bound the tail.
			tail := out[i:]
			if len(tail) > maxTail {
				return nil
			}
			return tail
		}

		body := string(out[i+2 : end-(boolToInt(out[end] == '\\'))])
		// body now reads e.g. "9;Message" or "777;notify;Title;Message".
		handleOSCBody(sess, store, push, body)
		out = out[end+1:]
	}
}

func boolToInt(b bool) int {
	if b {
		return 1
	}
	return 0
}

func handleOSCBody(sess *Session, store *SessionStore, push *PushManager, body string) {
	if body == "" {
		return
	}
	// Body format: "<code>;<payload>". Pull the code prefix.
	semi := strings.IndexByte(body, ';')
	if semi < 0 {
		return
	}
	code := body[:semi]
	payload := body[semi+1:]

	var reason string
	switch code {
	case "9", "99":
		// OSC 9 / 99: the whole payload is the message.
		reason = payload
	case "777":
		// OSC 777: "notify;<title>;<message>". Only fire on the "notify" subcommand.
		if !strings.HasPrefix(payload, "notify;") {
			return
		}
		rest := payload[len("notify;"):]
		// Prefer the message half if both title and message are present.
		if i := strings.IndexByte(rest, ';'); i >= 0 {
			reason = rest[i+1:]
		} else {
			reason = rest
		}
	default:
		return
	}
	reason = strings.TrimSpace(reason)
	if reason == "" {
		reason = "Agent is waiting for input"
	}
	_, edgeOn := store.SetAura(sess, true, reason)
	if !edgeOn {
		return
	}
	if push != nil && !desktopForegroundActive(sess) {
		push.Notify(NotificationPayload{
			Title:     sess.Name,
			Body:      reason,
			SessionID: sess.ID,
			Tag:       "aurex-" + sess.ID,
		})
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
			_, edgeOn := store.SetAura(sess, true, reason)
			tail.Reset()
			if !edgeOn {
				return
			}
			if push != nil && !desktopForegroundActive(sess) {
				push.Notify(NotificationPayload{
					Title:     sess.Name,
					Body:      reason,
					SessionID: sess.ID,
					Tag:       "aurex-" + sess.ID,
				})
			}
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
