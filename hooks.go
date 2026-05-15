package main

import (
	"encoding/json"
	"log"
	"net"
	"net/http"
	"strconv"
	"strings"
)

type hookPayload struct {
	Session string `json:"session"`
	Reason  string `json:"reason"`
	Active  bool   `json:"active"`
}

// parseHookPayload accepts both JSON and form-encoded bodies. Form encoding
// makes it easy to call this from a tmux `run-shell` hook without battling
// nested quotes — `curl -d active=true -d session=… -d reason=…` is enough.
func parseHookPayload(r *http.Request) (hookPayload, error) {
	var p hookPayload
	ct := r.Header.Get("Content-Type")
	if strings.Contains(ct, "application/json") {
		err := json.NewDecoder(r.Body).Decode(&p)
		return p, err
	}
	if err := r.ParseForm(); err != nil {
		return p, err
	}
	p.Session = r.Form.Get("session")
	p.Reason = r.Form.Get("reason")
	if v := r.Form.Get("active"); v != "" {
		p.Active, _ = strconv.ParseBool(v)
	}
	return p, nil
}

// hookAuraHandler is exposed at POST /api/hook/aura. It is intentionally unauthenticated
// but only accepts requests from localhost — agents on the box can call it directly.
func hookAuraHandler(store *SessionStore, push *PushManager) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if !isLocalRequest(r) {
			http.Error(w, "forbidden", http.StatusForbidden)
			return
		}
		payload, err := parseHookPayload(r)
		if err != nil {
			http.Error(w, "bad body", http.StatusBadRequest)
			return
		}

		sess := resolveSession(store, payload.Session)
		if sess == nil {
			// Common cause: hook payload omitted "session" and multiple
			// sessions exist, so resolveSession can't disambiguate. Log
			// helpful diagnostics rather than a silent 404.
			names := make([]string, 0)
			for _, s := range store.List() {
				names = append(names, s.TmuxName)
			}
			log.Printf("hook: no session match for %q (active sessions: %v)", payload.Session, names)
			http.Error(w, "session not found — pass \"session\": \"<tmux-name>\" in payload", http.StatusNotFound)
			return
		}

		if payload.Active {
			reason := payload.Reason
			if reason == "" {
				reason = "Agent is waiting for input"
			}
			store.SetAura(sess, true, reason)
			// Push suppression: skip phone notifications when a laptop/desktop
			// browser has aurex's tab in the foreground (visibility=visible).
			// In that case the user is at the laptop watching the aura ring
			// directly and a phone buzz is just noise.
			if push != nil && !desktopForegroundActive(sess) {
				push.Notify(NotificationPayload{
					Title:     sess.Name,
					Body:      reason,
					SessionID: sess.ID,
					Tag:       "aurex-" + sess.ID,
				})
			} else if push != nil {
				log.Printf("hook: %s aura set; push suppressed (laptop foreground)", sess.TmuxName)
			}
		} else {
			store.ClearAura(sess)
		}
		w.WriteHeader(http.StatusNoContent)
	}
}

// desktopForegroundActive reports whether any current WebSocket subscriber
// to this session is a laptop/desktop browser with its tab in the foreground.
// Drives push suppression — if you're at the laptop watching aurex, we don't
// also buzz your phone.
func desktopForegroundActive(sess *Session) bool {
	sess.subMu.Lock()
	defer sess.subMu.Unlock()
	for sub := range sess.activeSubs {
		if sub.IsForegroundDesktop() {
			return true
		}
	}
	return false
}

// resolveSession picks a session from the hook payload. If session is empty and
// only one session exists, that one is used; otherwise tries to match by ID or
// tmux name.
func resolveSession(store *SessionStore, key string) *Session {
	if key != "" {
		if sess := store.FindByTmuxName(key); sess != nil {
			return sess
		}
		return nil
	}
	sessions := store.List()
	if len(sessions) == 1 {
		return sessions[0]
	}
	return nil
}

func isLocalRequest(r *http.Request) bool {
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		host = r.RemoteAddr
	}
	if host == "" {
		return false
	}
	if host == "127.0.0.1" || host == "::1" || strings.HasPrefix(host, "127.") {
		return true
	}
	ip := net.ParseIP(host)
	if ip != nil && ip.IsLoopback() {
		return true
	}
	return false
}
