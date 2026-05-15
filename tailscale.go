package main

import (
	"encoding/json"
	"fmt"
	"os/exec"
	"strings"
)

type tailscaleStatus struct {
	BackendState string `json:"BackendState"`
	Self         struct {
		DNSName string `json:"DNSName"`
	} `json:"Self"`
	CurrentTailnet struct {
		MagicDNSSuffix  string `json:"MagicDNSSuffix"`
		MagicDNSEnabled bool   `json:"MagicDNSEnabled"`
	} `json:"CurrentTailnet"`
}

// TailscaleFQDN returns the MagicDNS hostname of this node (e.g.
// "laptop.tail-abcde.ts.net"). Returns an error with a user-readable reason
// when Tailscale isn't usable for cert provisioning.
func TailscaleFQDN() (string, error) {
	if _, err := exec.LookPath("tailscale"); err != nil {
		return "", fmt.Errorf("tailscale CLI not in PATH")
	}
	out, err := exec.Command("tailscale", "status", "--json").Output()
	if err != nil {
		return "", fmt.Errorf("tailscale status: %w", err)
	}
	var s tailscaleStatus
	if err := json.Unmarshal(out, &s); err != nil {
		return "", fmt.Errorf("parse tailscale status: %w", err)
	}
	if s.BackendState != "Running" {
		return "", fmt.Errorf("tailscale daemon not running (state: %s)", s.BackendState)
	}
	if !s.CurrentTailnet.MagicDNSEnabled {
		return "", fmt.Errorf("tailscale MagicDNS is disabled in the admin console")
	}
	fqdn := strings.TrimSuffix(s.Self.DNSName, ".")
	if fqdn == "" {
		return "", fmt.Errorf("tailscale status returned no DNSName")
	}
	return fqdn, nil
}

// TailscaleEnsureCert provisions (or refreshes) a Let's Encrypt cert for fqdn
// via the Tailscale daemon. Cert and key are written as PEM to the given paths.
// Requires the tailnet admin to have enabled HTTPS certificates.
func TailscaleEnsureCert(fqdn, certFile, keyFile string) error {
	if certFile == "" || keyFile == "" {
		return fmt.Errorf("cert/key file paths required")
	}
	cmd := exec.Command("tailscale", "cert",
		"--cert-file="+certFile,
		"--key-file="+keyFile,
		fqdn,
	)
	out, err := cmd.CombinedOutput()
	if err != nil {
		msg := strings.TrimSpace(string(out))
		if strings.Contains(msg, "HTTPS") || strings.Contains(msg, "Disable") {
			return fmt.Errorf("tailscale HTTPS not enabled — turn on HTTPS certificates in the tailnet admin console: %s", msg)
		}
		return fmt.Errorf("tailscale cert: %w: %s", err, msg)
	}
	return nil
}
