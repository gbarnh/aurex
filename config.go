package main

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"

	webpush "github.com/SherClockHolmes/webpush-go"
)

type Config struct {
	Port            int    `json:"port"`
	Auth            bool   `json:"auth"`
	Username        string `json:"username"`
	Password        string `json:"password"`
	VapidPublicKey  string `json:"vapidPublicKey"`
	VapidPrivateKey string `json:"vapidPrivateKey"`
	DefaultShell    string `json:"defaultShell"`
	TmuxPrefix      string `json:"tmuxPrefix"`
	TLS             bool   `json:"tls"`
	TLSCertFile     string `json:"tlsCertFile"`
	TLSKeyFile      string `json:"tlsKeyFile"`

	// HTTPRedirectPort, when non-zero and TLS is on, starts a sibling HTTP
	// server that 301-redirects every request to the HTTPS origin. Default
	// 7680 (one below the main port, no privileges needed). Set to 80 and
	// run aurex with capability/root to capture browsers typing http://host.
	HTTPRedirectPort int `json:"httpRedirectPort"`

	// Tailscale: "auto" tries to use the tailnet for a real cert and falls
	// back to self-signed; "on" requires Tailscale and fails if unavailable;
	// "off" disables. Cert/key files default to aurex.ts.cert.pem/key.pem.
	Tailscale         string `json:"tailscale"`
	TailscaleCertFile string `json:"tailscaleCertFile"`
	TailscaleKeyFile  string `json:"tailscaleKeyFile"`

	PushSubscriptionsFile string `json:"pushSubscriptionsFile"`

	path string
}

func defaultConfigPath() string {
	if p := os.Getenv("AUREX_CONFIG"); p != "" {
		return p
	}
	return "aurex.config.json"
}

func LoadConfig() (*Config, error) {
	path := defaultConfigPath()
	cfg := &Config{
		Port:              7681,
		Auth:              false,
		Username:          "aurex",
		Password:          "changeme",
		DefaultShell:      "bash",
		TmuxPrefix:        "aurex",
		TLS:               true,
		TLSCertFile:       "aurex.cert.pem",
		TLSKeyFile:        "aurex.key.pem",
		HTTPRedirectPort:  7680,
		Tailscale:             "auto",
		TailscaleCertFile:     "aurex.ts.cert.pem",
		TailscaleKeyFile:      "aurex.ts.key.pem",
		PushSubscriptionsFile: "aurex.subscriptions.json",
		path:              path,
	}

	data, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		if err := cfg.ensureVapid(); err != nil {
			return nil, err
		}
		if err := cfg.Save(); err != nil {
			return nil, err
		}
		fmt.Printf("aurex: wrote default config to %s\n", path)
		return cfg, nil
	}
	if err != nil {
		return nil, err
	}

	if err := json.Unmarshal(data, cfg); err != nil {
		return nil, fmt.Errorf("parse config: %w", err)
	}
	cfg.path = path

	dirty := false
	if cfg.VapidPublicKey == "" || cfg.VapidPrivateKey == "" {
		if err := cfg.ensureVapid(); err != nil {
			return nil, err
		}
		dirty = true
	}
	if cfg.DefaultShell == "" {
		cfg.DefaultShell = "bash"
		dirty = true
	}
	if cfg.TmuxPrefix == "" {
		cfg.TmuxPrefix = "aurex"
		dirty = true
	}
	if cfg.Port == 0 {
		cfg.Port = 7681
		dirty = true
	}
	if cfg.TLS && cfg.TLSCertFile == "" {
		cfg.TLSCertFile = "aurex.cert.pem"
		dirty = true
	}
	if cfg.TLS && cfg.TLSKeyFile == "" {
		cfg.TLSKeyFile = "aurex.key.pem"
		dirty = true
	}
	if cfg.Tailscale == "" {
		cfg.Tailscale = "auto"
		dirty = true
	}
	if cfg.TailscaleCertFile == "" {
		cfg.TailscaleCertFile = "aurex.ts.cert.pem"
		dirty = true
	}
	if cfg.TailscaleKeyFile == "" {
		cfg.TailscaleKeyFile = "aurex.ts.key.pem"
		dirty = true
	}
	if cfg.PushSubscriptionsFile == "" {
		cfg.PushSubscriptionsFile = "aurex.subscriptions.json"
		dirty = true
	}
	if dirty {
		if err := cfg.Save(); err != nil {
			return nil, err
		}
	}
	return cfg, nil
}

func (c *Config) ensureVapid() error {
	priv, pub, err := webpush.GenerateVAPIDKeys()
	if err != nil {
		return fmt.Errorf("generate vapid keys: %w", err)
	}
	c.VapidPrivateKey = priv
	c.VapidPublicKey = pub
	return nil
}

func (c *Config) Save() error {
	data, err := json.MarshalIndent(c, "", "  ")
	if err != nil {
		return err
	}
	if dir := filepath.Dir(c.path); dir != "" && dir != "." {
		_ = os.MkdirAll(dir, 0o755)
	}
	return os.WriteFile(c.path, data, 0o600)
}
