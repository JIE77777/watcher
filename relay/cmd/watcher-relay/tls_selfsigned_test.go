package main

import (
	"crypto/x509"
	"encoding/pem"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestEnsureRelayTLSCertificateGeneratesFilesAndFingerprint(t *testing.T) {
	dir := t.TempDir()
	cfg := Config{
		BindAddr: "0.0.0.0:8780",
		Security: SecurityConfig{
			AllowedHosts: []string{"relay.example.com", "127.0.0.1"},
			TLS: TLSConfig{
				Enabled:         true,
				AutoSelfSigned:  true,
				CertFile:        filepath.Join(dir, "relay.crt"),
				KeyFile:         filepath.Join(dir, "relay.key"),
				FingerprintFile: filepath.Join(dir, "fingerprint.txt"),
			},
		},
	}
	cfg.Security.TLS.Hosts = defaultRelayTLSHosts(cfg)

	if err := ensureRelayTLSCertificate(&cfg); err != nil {
		t.Fatalf("ensure tls certificate: %v", err)
	}
	if _, err := os.Stat(cfg.Security.TLS.CertFile); err != nil {
		t.Fatalf("cert not written: %v", err)
	}
	if _, err := os.Stat(cfg.Security.TLS.KeyFile); err != nil {
		t.Fatalf("key not written: %v", err)
	}
	fingerprint, err := os.ReadFile(cfg.Security.TLS.FingerprintFile)
	if err != nil {
		t.Fatalf("fingerprint not written: %v", err)
	}
	if !strings.HasPrefix(strings.TrimSpace(string(fingerprint)), "SHA256:") {
		t.Fatalf("unexpected fingerprint %q", string(fingerprint))
	}

	certData, err := os.ReadFile(cfg.Security.TLS.CertFile)
	if err != nil {
		t.Fatal(err)
	}
	block, _ := pem.Decode(certData)
	if block == nil {
		t.Fatal("certificate PEM block missing")
	}
	cert, err := x509.ParseCertificate(block.Bytes)
	if err != nil {
		t.Fatalf("parse certificate: %v", err)
	}
	if !stringSliceContains(cert.DNSNames, "relay.example.com") {
		t.Fatalf("expected relay.example.com in DNS SANs: %#v", cert.DNSNames)
	}
	if len(cert.IPAddresses) == 0 {
		t.Fatalf("expected IP SANs")
	}
}

func TestNormalizedTLSHosts(t *testing.T) {
	hosts := normalizedTLSHosts([]string{
		"https://relay.example.com:8780/install",
		"relay.example.com",
		"http://127.0.0.1:8780",
		"",
	})
	if !stringSliceContains(hosts, "relay.example.com") {
		t.Fatalf("missing relay.example.com: %#v", hosts)
	}
	if !stringSliceContains(hosts, "127.0.0.1") {
		t.Fatalf("missing 127.0.0.1: %#v", hosts)
	}
}

func stringSliceContains(values []string, needle string) bool {
	for _, value := range values {
		if value == needle {
			return true
		}
	}
	return false
}
