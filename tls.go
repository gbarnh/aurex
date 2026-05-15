package main

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"fmt"
	"math/big"
	"net"
	"os"
	"path/filepath"
	"time"
)

// EnsureSelfSignedCert generates a self-signed TLS cert if certFile and keyFile
// don't already exist. The cert covers localhost, loopback, and every non-link-local
// IPv4/IPv6 address bound to a system interface — so it works for both desktop
// (localhost) and phone (LAN IP) access without further config.
func EnsureSelfSignedCert(certFile, keyFile string) error {
	if certFile == "" {
		certFile = "aurex.cert.pem"
	}
	if keyFile == "" {
		keyFile = "aurex.key.pem"
	}
	if fileExists(certFile) && fileExists(keyFile) {
		return nil
	}

	priv, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return fmt.Errorf("generate key: %w", err)
	}

	serial, err := rand.Int(rand.Reader, new(big.Int).Lsh(big.NewInt(1), 128))
	if err != nil {
		return fmt.Errorf("serial: %w", err)
	}

	// Self-signed cert that doubles as its own CA so Android's "Install CA
	// certificate" flow accepts it. Without CA:TRUE + KeyCertSign, Android
	// won't install it as a CA and falls through to the User-cert flow which
	// demands a private key.
	//
	// Validity capped at 800 days (under iOS Safari's 825-day server-cert
	// limit). KeyEncipherment intentionally omitted — invalid for ECDSA and
	// rejected by strict mobile validators.
	tmpl := x509.Certificate{
		SerialNumber:          serial,
		Subject:               pkix.Name{CommonName: "aurex", Organization: []string{"aurex"}},
		NotBefore:             time.Now().Add(-1 * time.Hour),
		NotAfter:              time.Now().AddDate(0, 0, 800),
		KeyUsage:              x509.KeyUsageDigitalSignature | x509.KeyUsageCertSign,
		ExtKeyUsage:           []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
		IsCA:                  true,
		BasicConstraintsValid: true,
		MaxPathLen:            0,
		MaxPathLenZero:        true,
		DNSNames:              []string{"localhost"},
		IPAddresses:           collectLocalIPs(),
		SignatureAlgorithm:    x509.ECDSAWithSHA256,
	}

	derBytes, err := x509.CreateCertificate(rand.Reader, &tmpl, &tmpl, &priv.PublicKey, priv)
	if err != nil {
		return fmt.Errorf("create cert: %w", err)
	}

	if dir := filepath.Dir(certFile); dir != "" && dir != "." {
		_ = os.MkdirAll(dir, 0o755)
	}

	certOut, err := os.OpenFile(certFile, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0o644)
	if err != nil {
		return fmt.Errorf("open cert: %w", err)
	}
	if err := pem.Encode(certOut, &pem.Block{Type: "CERTIFICATE", Bytes: derBytes}); err != nil {
		certOut.Close()
		return err
	}
	if err := certOut.Close(); err != nil {
		return err
	}

	keyOut, err := os.OpenFile(keyFile, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0o600)
	if err != nil {
		return fmt.Errorf("open key: %w", err)
	}
	keyDER, err := x509.MarshalECPrivateKey(priv)
	if err != nil {
		keyOut.Close()
		return err
	}
	if err := pem.Encode(keyOut, &pem.Block{Type: "EC PRIVATE KEY", Bytes: keyDER}); err != nil {
		keyOut.Close()
		return err
	}
	return keyOut.Close()
}

func fileExists(path string) bool {
	_, err := os.Stat(path)
	return err == nil
}

// collectLocalIPs returns loopback + all non-link-local IPs bound to system
// interfaces so the cert is valid for both 127.0.0.1 and the LAN address the
// phone uses.
func collectLocalIPs() []net.IP {
	ips := []net.IP{net.IPv4(127, 0, 0, 1), net.IPv6loopback}
	addrs, err := net.InterfaceAddrs()
	if err != nil {
		return ips
	}
	for _, a := range addrs {
		ipnet, ok := a.(*net.IPNet)
		if !ok {
			continue
		}
		ip := ipnet.IP
		if ip.IsLoopback() || ip.IsLinkLocalUnicast() || ip.IsLinkLocalMulticast() {
			continue
		}
		ips = append(ips, ip)
	}
	return ips
}
