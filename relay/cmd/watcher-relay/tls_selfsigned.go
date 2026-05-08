package main

import (
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/hex"
	"encoding/pem"
	"fmt"
	"math/big"
	"net"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

func ensureRelayTLSCertificate(cfg *Config) error {
	tls := cfg.Security.TLS
	if tls.CertFile == "" || tls.KeyFile == "" {
		return fmt.Errorf("cert_file and key_file are required when tls.enabled=true")
	}
	certExists := fileExists(tls.CertFile)
	keyExists := fileExists(tls.KeyFile)
	if certExists && keyExists {
		return writeRelayFingerprint(tls.CertFile, tls.FingerprintFile)
	}
	if !tls.AutoSelfSigned {
		return fmt.Errorf("tls certificate is missing and auto_self_signed=false")
	}
	if err := os.MkdirAll(filepath.Dir(tls.CertFile), 0o700); err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(tls.KeyFile), 0o700); err != nil {
		return err
	}
	certPEM, keyPEM, err := generateSelfSignedRelayCertificate(tls.Hosts)
	if err != nil {
		return err
	}
	if err := os.WriteFile(tls.CertFile, certPEM, 0o600); err != nil {
		return err
	}
	if err := os.WriteFile(tls.KeyFile, keyPEM, 0o600); err != nil {
		return err
	}
	return writeRelayFingerprint(tls.CertFile, tls.FingerprintFile)
}

func generateSelfSignedRelayCertificate(hosts []string) ([]byte, []byte, error) {
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		return nil, nil, err
	}
	serialLimit := new(big.Int).Lsh(big.NewInt(1), 128)
	serial, err := rand.Int(rand.Reader, serialLimit)
	if err != nil {
		return nil, nil, err
	}
	notBefore := time.Now().Add(-5 * time.Minute)
	notAfter := notBefore.Add(3650 * 24 * time.Hour)
	template := x509.Certificate{
		SerialNumber: serial,
		Subject: pkix.Name{
			CommonName: "Watcher Relay",
		},
		NotBefore:             notBefore,
		NotAfter:              notAfter,
		KeyUsage:              x509.KeyUsageDigitalSignature | x509.KeyUsageKeyEncipherment,
		ExtKeyUsage:           []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
		BasicConstraintsValid: true,
	}
	for _, host := range normalizedTLSHosts(hosts) {
		if ip := net.ParseIP(host); ip != nil {
			template.IPAddresses = append(template.IPAddresses, ip)
		} else {
			template.DNSNames = append(template.DNSNames, host)
		}
	}
	if len(template.IPAddresses) == 0 && len(template.DNSNames) == 0 {
		template.IPAddresses = []net.IP{net.ParseIP("127.0.0.1")}
		template.DNSNames = []string{"localhost"}
	}
	der, err := x509.CreateCertificate(rand.Reader, &template, &template, &key.PublicKey, key)
	if err != nil {
		return nil, nil, err
	}
	certPEM := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der})
	keyPEM := pem.EncodeToMemory(&pem.Block{Type: "RSA PRIVATE KEY", Bytes: x509.MarshalPKCS1PrivateKey(key)})
	return certPEM, keyPEM, nil
}

func relayCertificateFingerprint(certFile string) (string, error) {
	data, err := os.ReadFile(certFile)
	if err != nil {
		return "", err
	}
	block, _ := pem.Decode(data)
	if block == nil {
		return "", fmt.Errorf("no certificate PEM block found")
	}
	sum := sha256.Sum256(block.Bytes)
	encoded := strings.ToUpper(hex.EncodeToString(sum[:]))
	parts := make([]string, 0, len(encoded)/2)
	for i := 0; i+2 <= len(encoded); i += 2 {
		parts = append(parts, encoded[i:i+2])
	}
	return "SHA256:" + strings.Join(parts, ":"), nil
}

func writeRelayFingerprint(certFile, fingerprintFile string) error {
	if fingerprintFile == "" {
		return nil
	}
	fingerprint, err := relayCertificateFingerprint(certFile)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(fingerprintFile), 0o700); err != nil {
		return err
	}
	return os.WriteFile(fingerprintFile, []byte(fingerprint+"\n"), 0o600)
}

func defaultRelayTLSHosts(cfg Config) []string {
	hosts := []string{"localhost", "127.0.0.1", "::1"}
	if host, _, err := net.SplitHostPort(cfg.BindAddr); err == nil {
		if host != "" && host != "0.0.0.0" && host != "::" {
			hosts = append(hosts, host)
		}
	}
	for _, host := range cfg.Security.AllowedHosts {
		host = strings.TrimSpace(host)
		if host != "" && !strings.Contains(host, "*") {
			hosts = append(hosts, host)
		}
	}
	return normalizedTLSHosts(hosts)
}

func normalizedTLSHosts(hosts []string) []string {
	seen := map[string]bool{}
	out := make([]string, 0, len(hosts))
	for _, host := range hosts {
		host = strings.TrimSpace(strings.TrimPrefix(host, "https://"))
		host = strings.TrimPrefix(host, "http://")
		if before, _, ok := strings.Cut(host, "/"); ok {
			host = before
		}
		if parsedHost, _, err := net.SplitHostPort(host); err == nil {
			host = parsedHost
		}
		host = strings.Trim(host, "[]")
		if host == "" || seen[host] {
			continue
		}
		seen[host] = true
		out = append(out, host)
	}
	sort.Strings(out)
	return out
}

func fileExists(path string) bool {
	if path == "" {
		return false
	}
	_, err := os.Stat(path)
	return err == nil
}
