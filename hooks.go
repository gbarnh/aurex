package main

import (
	"encoding/json"
	"log"
	"net"
	"net/http"
	"strings"
)

type hookPayload struct {
	Session string `json:"session"`
	Reason  string `json:"reason"`
	Active  bool   `json:"active"`
}

// hookAuraHandler is exposed at POST /api/hook/aura. It is intentionally unauthenticated
// but only accepts requests from localhost — agents on the box can call it directly.
func hookAuraHandler(store *SessionStore, push *PushManager) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if !isLocalRequest(r) {
			http.Error(w, "forbidden", http.StatusForbidden)
			return
		}
		var payload hookPayload
		if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
			http.Error(w, "bad json", http.StatusBadRequest)
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
			if push != nil {
				push.Notify(NotificationPayload{
					Title:     sess.Name,
					Body:      reason,
					SessionID: sess.ID,
					Tag:       "aurex-" + sess.ID,
				})
			}
		} else {
			store.ClearAura(sess)
		}
		w.WriteHeader(http.StatusNoContent)
	}
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
