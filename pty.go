package main

import (
	"encoding/json"
	"os/exec"
	"strings"

	"github.com/gorilla/websocket"
)

// forceTmuxRedraw finds the tmux client attached to the given session and
// asks tmux to redraw it. Used on subscriber connect so the client's canvas
// gets painted with the current screen state at the current size.
//
// The redraw bytes flow through runCapture and broadcast to every subscriber
// (the new one and any others). This avoids the snapshot/size race we hit
// with capture-pane — tmux only redraws once it has actually applied the
// new size, so the bytes are guaranteed to be at the right dimensions.
func forceTmuxRedraw(tmuxName string) {
	out, err := exec.Command("tmux", "list-clients", "-t", tmuxName, "-F", "#{client_name}").Output()
	if err != nil {
		return
	}
	first := strings.SplitN(strings.TrimSpace(string(out)), "\n", 2)[0]
	if first == "" {
		return
	}
	_ = exec.Command("tmux", "refresh-client", "-t", first).Run()
}

// AttachSubscriber wires a WebSocket conn to an existing session.
//
// Protocol on connect:
//  1. Register the WS as a subscriber so any subsequent bytes broadcast to us.
//  2. Push a screen-clear to ONLY this sub so any leftover pixels (from a
//     previous session in the same ghostty instance, or stale paint) get
//     wiped before fresh bytes arrive.
//  3. Trigger a tmux redraw to the attached client. The redraw bytes flow
//     through runCapture and broadcast, repainting this sub's screen — and
//     any other connected sub's screen — at the current PTY size.
//
// cursor is read from the URL but currently unused; kept for protocol
// compatibility and possible future replay support.
func AttachSubscriber(sess *Session, store *SessionStore, conn *websocket.Conn, cursor int64) {
	_ = cursor

	sub := newSubscriber(conn)

	sess.subMu.Lock()
	sess.activeSubs[sub] = true
	sess.subMu.Unlock()

	defer func() {
		sess.subMu.Lock()
		delete(sess.activeSubs, sub)
		sess.subMu.Unlock()
		close(sub.send)
	}()

	go sub.writer()

	// Per-sub clear: hide cursor, home, clear screen, show cursor. Sent only
	// to this sub (not broadcast) so it doesn't flicker existing viewers.
	sub.Push(SubscriberMessage{
		Type:   "output",
		Data:   "\x1b[?25l\x1b[H\x1b[2J\x1b[?25h",
		Cursor: sess.buffer.Cursor(),
	})

	// Force tmux to redraw to its attached client. Those bytes flow through
	// runCapture → broadcast → this sub, painting current state at the size
	// the server-side resize just applied.
	forceTmuxRedraw(sess.TmuxName)

	for {
		_, raw, err := conn.ReadMessage()
		if err != nil {
			return
		}
		var msg clientMessage
		if err := json.Unmarshal(raw, &msg); err != nil {
			continue
		}
		switch msg.Type {
		case "input":
			var s string
			if err := json.Unmarshal(msg.Data, &s); err != nil {
				continue
			}
			WriteInput(sess, store, s)
		case "resize":
			ResizePTY(sess, msg.Cols, msg.Rows)
		}
	}
}
