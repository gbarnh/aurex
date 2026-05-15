package main

import (
	"crypto/subtle"
	"encoding/json"
	"io"
	"io/fs"
	"log"
	"net/http"
	"strconv"
	"strings"

	"github.com/go-chi/chi/v5"
	"github.com/gorilla/websocket"
)

type Server struct {
	cfg      *Config
	store    *SessionStore
	push     *PushManager
	frontend fs.FS // embedded React build (may be nil if not embedded yet)
	upgrader websocket.Upgrader
}

func NewServer(cfg *Config, store *SessionStore, push *PushManager, frontend fs.FS) *Server {
	return &Server{
		cfg:      cfg,
		store:    store,
		push:     push,
		frontend: frontend,
		upgrader: websocket.Upgrader{
			ReadBufferSize:  4096,
			WriteBufferSize: 4096,
			// Local-network tool — we don't enforce origin.
			CheckOrigin: func(r *http.Request) bool { return true },
		},
	}
}

func (s *Server) Routes() http.Handler {
	r := chi.NewRouter()

	r.Get("/ws/{sessionID}", s.authWS(s.handleWS))

	r.Route("/api", func(r chi.Router) {
		r.Use(s.authMiddleware)
		r.Get("/sessions", s.handleListSessions)
		r.Post("/sessions", s.handleCreateSession)
		r.Patch("/sessions/{sessionID}", s.handleRenameSession)
		r.Delete("/sessions/{sessionID}", s.handleDeleteSession)
		r.Get("/push/vapid-public-key", s.handleVapidPublicKey)
		r.Post("/push/subscribe", s.handleSubscribe)
		r.Post("/push/test", s.handlePushTest)
	})

	// Cert download for one-tap install on phones. Auth-free so the user can
	// grab it before notifications work. Only available when TLS is on.
	if s.cfg.TLS && s.cfg.TLSCertFile != "" {
		r.Get("/aurex.cert.pem", s.handleCertDownload)
	}

	// Hook endpoint is intentionally NOT behind auth — it gates on localhost instead.
	r.Post("/api/hook/aura", hookAuraHandler(s.store, s.push))

	// Embedded frontend last so /api/* and /ws/* take precedence.
	r.Handle("/*", s.frontendHandler())

	return r
}

func (s *Server) authMiddleware(next http.Handler) http.Handler {
	if !s.cfg.Auth {
		return next
	}
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		u, p, ok := r.BasicAuth()
		if !ok ||
			subtle.ConstantTimeCompare([]byte(u), []byte(s.cfg.Username)) != 1 ||
			subtle.ConstantTimeCompare([]byte(p), []byte(s.cfg.Password)) != 1 {
			w.Header().Set("WWW-Authenticate", `Basic realm="aurex"`)
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}
		next.ServeHTTP(w, r)
	})
}

func (s *Server) authWS(h http.HandlerFunc) http.HandlerFunc {
	if !s.cfg.Auth {
		return h
	}
	return func(w http.ResponseWriter, r *http.Request) {
		u, p, ok := r.BasicAuth()
		if !ok ||
			subtle.ConstantTimeCompare([]byte(u), []byte(s.cfg.Username)) != 1 ||
			subtle.ConstantTimeCompare([]byte(p), []byte(s.cfg.Password)) != 1 {
			w.Header().Set("WWW-Authenticate", `Basic realm="aurex"`)
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}
		h(w, r)
	}
}

func (s *Server) handleListSessions(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, map[string]any{"sessions": s.store.List()})
}

func (s *Server) handleCreateSession(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Name string `json:"name"`
	}
	if r.Body != nil {
		_ = json.NewDecoder(r.Body).Decode(&body)
	}
	sess, err := s.store.Create(body.Name)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	writeJSON(w, http.StatusCreated, sess)
}

func (s *Server) handleRenameSession(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "sessionID")
	var body struct {
		Name string `json:"name"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		http.Error(w, "bad json", http.StatusBadRequest)
		return
	}
	if err := s.store.Rename(id, body.Name); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	if sess := s.store.Get(id); sess != nil {
		writeJSON(w, http.StatusOK, sess)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) handleDeleteSession(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "sessionID")
	if err := s.store.Delete(id); err != nil {
		http.Error(w, err.Error(), http.StatusNotFound)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) handleVapidPublicKey(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, map[string]string{"publicKey": s.push.PublicKey()})
}

func (s *Server) handleCertDownload(w http.ResponseWriter, r *http.Request) {
	// application/x-x509-ca-cert + .crt filename tells Android this is a CA cert,
	// not a user/client cert. With a .pem extension Android's installer would
	// default to the user-cert flow and ask for a private key.
	w.Header().Set("Content-Type", "application/x-x509-ca-cert")
	w.Header().Set("Content-Disposition", `attachment; filename="aurex.crt"`)
	http.ServeFile(w, r, s.cfg.TLSCertFile)
}

func (s *Server) handlePushTest(w http.ResponseWriter, r *http.Request) {
	res := s.push.Notify(NotificationPayload{
		Title:     "aurex test",
		Body:      "Push pipeline is working.",
		SessionID: "",
		Tag:       "aurex-test",
	})
	log.Printf("push: test → total=%d sent=%d failed=%d pruned=%d lastErr=%q",
		res.Total, res.Sent, res.Failed, res.Pruned, res.LastErr)
	writeJSON(w, http.StatusOK, res)
}

func (s *Server) handleSubscribe(w http.ResponseWriter, r *http.Request) {
	body, err := io.ReadAll(r.Body)
	if err != nil {
		http.Error(w, "read body", http.StatusBadRequest)
		return
	}
	sub, err := SubscriptionFromJSON(body)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	s.push.AddSubscription(sub)
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) handleWS(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "sessionID")
	sess := s.store.Get(id)
	if sess == nil {
		http.Error(w, "session not found", http.StatusNotFound)
		return
	}
	q := r.URL.Query()
	var cursor int64
	if c := q.Get("cursor"); c != "" {
		if n, err := strconv.ParseInt(c, 10, 64); err == nil && n >= 0 {
			cursor = n
		}
	}
	cols, _ := strconv.Atoi(q.Get("cols"))
	rows, _ := strconv.Atoi(q.Get("rows"))
	conn, err := s.upgrader.Upgrade(w, r, nil)
	if err != nil {
		log.Printf("ws upgrade: %v", err)
		return
	}
	// Resize the PTY to the client's reported size BEFORE attaching, so any
	// snapshot or redraw we capture is at the client's actual dimensions
	// instead of whatever the previous client left the PTY at.
	if cols > 0 && rows > 0 {
		ResizePTY(sess, cols, rows)
	}
	AttachSubscriber(sess, s.store, conn, cursor)
}

// BroadcastSessionUpdate fans out session metadata changes (name, aura, cwd,
// branch) to every connected WebSocket subscriber across all sessions. Uses
// the per-subscriber send channel so it doesn't block on slow clients.
func (s *Server) BroadcastSessionUpdate(sess *Session) {
	sessJSON, _ := json.Marshal(sess)
	updateBytes, _ := json.Marshal(map[string]any{
		"type":    "session_update",
		"session": json.RawMessage(sessJSON),
	})
	listMsg := map[string]any{"type": "sessions_list", "sessions": s.store.List()}
	listBytes, _ := json.Marshal(listMsg)

	all := s.store.List()
	seen := make(map[*Subscriber]bool)
	for _, target := range all {
		target.subMu.Lock()
		subs := make([]*Subscriber, 0, len(target.activeSubs))
		for sub := range target.activeSubs {
			subs = append(subs, sub)
		}
		target.subMu.Unlock()
		for _, sub := range subs {
			if seen[sub] {
				continue
			}
			seen[sub] = true
			// We can't use Push here because Push is keyed to SubscriberMessage
			// JSON shape (output, cursor). Drop control messages directly into
			// the writer via a raw-payload variant.
			_ = pushRaw(sub, listBytes)
		}
		// Only this session's subscribers also get the per-session update.
		for _, sub := range subs {
			_ = pushRaw(sub, updateBytes)
		}
	}
}

// pushRaw sends an already-encoded JSON payload to a subscriber's writer
// goroutine via a non-blocking channel write.
func pushRaw(sub *Subscriber, payload []byte) bool {
	select {
	case sub.send <- SubscriberMessage{rawPayload: payload}:
		return true
	default:
		return false
	}
}

func (s *Server) frontendHandler() http.Handler {
	if s.frontend == nil {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			http.Error(w, "frontend not embedded — build with `cd client && npm run build`", http.StatusNotFound)
		})
	}
	fileServer := http.FileServer(http.FS(s.frontend))
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// SPA fallback: if the request doesn't map to a file, serve index.html.
		p := strings.TrimPrefix(r.URL.Path, "/")
		if p == "" {
			r.URL.Path = "/"
			fileServer.ServeHTTP(w, r)
			return
		}
		if _, err := fs.Stat(s.frontend, p); err != nil {
			r.URL.Path = "/"
			fileServer.ServeHTTP(w, r)
			return
		}
		fileServer.ServeHTTP(w, r)
	})
}

func writeJSON(w http.ResponseWriter, status int, body any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(body)
}
