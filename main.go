package main

import (
	"context"
	"errors"
	"fmt"
	"log"
	"net"
	"net/http"
	"os"
	"os/exec"
	"os/signal"
	"strings"
	"syscall"
	"time"
)

// version is overwritten at build time via -ldflags=-X main.version=…
// (see .github/workflows/release.yml).
var version = "dev"

func main() {
	if len(os.Args) > 1 && (os.Args[1] == "--version" || os.Args[1] == "-v") {
		fmt.Println("aurex", version)
		return
	}
	log.Printf("aurex %s starting", version)
	cfg, err := LoadConfig()
	if err != nil {
		log.Fatalf("aurex: load config: %v", err)
	}

	if _, err := exec.LookPath("tmux"); err != nil {
		log.Fatalf("aurex: tmux not found in PATH — install tmux to use aurex")
	}

	push := NewPushManager(cfg.VapidPublicKey, cfg.VapidPrivateKey, cfg.PushSubscriptionsFile)
	store := NewSessionStore(cfg.TmuxPrefix, cfg.DefaultShell, push)
	if err := store.AdoptExisting(); err != nil {
		log.Printf("aurex: adopt existing tmux sessions: %v", err)
	}

	server := NewServer(cfg, store, push, Frontend())
	store.SetOnUpdate(server.BroadcastSessionUpdate)

	stop := make(chan struct{})
	go store.PollMetadata(3*time.Second, stop)

	addr := fmt.Sprintf(":%d", cfg.Port)
	httpServer := &http.Server{
		Addr:     addr,
		Handler:  server.Routes(),
		ErrorLog: log.New(quietTLSWriter{}, "", log.LstdFlags),
	}

	sigs := make(chan os.Signal, 1)
	signal.Notify(sigs, syscall.SIGINT, syscall.SIGTERM)
	go func() {
		<-sigs
		log.Printf("aurex: shutting down (tmux sessions remain alive)")
		close(stop)
		ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		defer cancel()
		_ = httpServer.Shutdown(ctx)
	}()

	certFile, keyFile := "", ""
	publicURL := ""
	usingTailscale := false

	if cfg.TLS {
		// 1) Try Tailscale first when requested.
		if cfg.Tailscale == "auto" || cfg.Tailscale == "on" {
			fqdn, err := TailscaleFQDN()
			if err == nil {
				if cerr := TailscaleEnsureCert(fqdn, cfg.TailscaleCertFile, cfg.TailscaleKeyFile); cerr == nil {
					certFile = cfg.TailscaleCertFile
					keyFile = cfg.TailscaleKeyFile
					publicURL = fmt.Sprintf("https://%s:%d", fqdn, cfg.Port)
					usingTailscale = true
					log.Printf("aurex: using Tailscale cert for %s (auto-renew on restart)", fqdn)
					go renewTailscaleCert(fqdn, cfg.TailscaleCertFile, cfg.TailscaleKeyFile, stop)
				} else if cfg.Tailscale == "on" {
					log.Fatalf("aurex: tailscale cert required but failed: %v", cerr)
				} else {
					log.Printf("aurex: tailscale cert unavailable (%v) — falling back to self-signed", cerr)
				}
			} else if cfg.Tailscale == "on" {
				log.Fatalf("aurex: tailscale required but unavailable: %v", err)
			} else if !strings.Contains(err.Error(), "not in PATH") {
				// Don't spam the log when tailscale just isn't installed.
				log.Printf("aurex: tailscale not usable (%v) — using self-signed", err)
			}
		}
		// 2) Self-signed fallback.
		if certFile == "" {
			if err := EnsureSelfSignedCert(cfg.TLSCertFile, cfg.TLSKeyFile); err != nil {
				log.Fatalf("aurex: self-signed cert: %v", err)
			}
			certFile = cfg.TLSCertFile
			keyFile = cfg.TLSKeyFile
			publicURL = fmt.Sprintf("https://<your-host>:%d", cfg.Port)
		}
	}

	if usingTailscale {
		log.Printf("aurex: open %s on your phone — real cert, no warnings", publicURL)
	} else if cfg.TLS {
		log.Printf("aurex: listening on https://0.0.0.0:%d (self-signed — install aurex.cert.pem on phone to avoid warnings)", cfg.Port)
	} else {
		log.Printf("aurex: listening on http://0.0.0.0:%d — push notifications require HTTPS", cfg.Port)
	}

	if cfg.TLS && cfg.HTTPRedirectPort > 0 {
		go runHTTPRedirect(cfg.HTTPRedirectPort, cfg.Port)
	}

	var serveErr error
	if cfg.TLS {
		serveErr = httpServer.ListenAndServeTLS(certFile, keyFile)
	} else {
		serveErr = httpServer.ListenAndServe()
	}
	if serveErr != nil && !errors.Is(serveErr, http.ErrServerClosed) {
		log.Fatalf("aurex: serve: %v", serveErr)
	}
	_ = publicURL
}

// quietTLSWriter drops TLS handshake noise (clients rejecting the self-signed
// cert, probes, etc.) but lets real http.Server errors through to stderr.
type quietTLSWriter struct{}

func (quietTLSWriter) Write(p []byte) (int, error) {
	s := string(p)
	if strings.Contains(s, "TLS handshake error") ||
		strings.Contains(s, "tls: unknown certificate") ||
		strings.Contains(s, "tls: client didn't provide a certificate") {
		return len(p), nil
	}
	return os.Stderr.Write(p)
}

// runHTTPRedirect serves an HTTP-only listener that 301-redirects everything
// to the same host on the HTTPS port. Lets phones/browsers that typed
// "http://host:port" land in the right place instead of seeing a connection
// reset from Go's TLS listener.
func runHTTPRedirect(httpPort, httpsPort int) {
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		host, _, err := net.SplitHostPort(r.Host)
		if err != nil || host == "" {
			host = r.Host
		}
		target := fmt.Sprintf("https://%s:%d%s", host, httpsPort, r.RequestURI)
		http.Redirect(w, r, target, http.StatusMovedPermanently)
	})
	addr := fmt.Sprintf(":%d", httpPort)
	log.Printf("aurex: HTTP→HTTPS redirect listening on :%d", httpPort)
	if err := http.ListenAndServe(addr, handler); err != nil {
		log.Printf("aurex: http redirect server: %v (continuing without redirect)", err)
	}
}

// renewTailscaleCert re-runs `tailscale cert` daily so long-running aurex
// instances pick up Let's Encrypt renewals (90-day lifetime). The daemon
// no-ops when the cert isn't near expiry, so this is cheap.
func renewTailscaleCert(fqdn, certFile, keyFile string, stop <-chan struct{}) {
	t := time.NewTicker(24 * time.Hour)
	defer t.Stop()
	for {
		select {
		case <-stop:
			return
		case <-t.C:
			if err := TailscaleEnsureCert(fqdn, certFile, keyFile); err != nil {
				log.Printf("aurex: tailscale cert renew: %v", err)
			}
		}
	}
}
