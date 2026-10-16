package server

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
	"time"
)

// EnsureCert выдаёт самоподписанный TLS-сертификат с SAN-именами хоста.
// Без https браузер запрещает getUserMedia — сканер QR требует secure context.
func EnsureCert(dir string) (certFile, keyFile string, err error) {
	certFile, keyFile = dir+"/tls.crt", dir+"/tls.key"
	if _, statErr := os.Stat(certFile); statErr == nil {
		return certFile, keyFile, nil
	}

	dns := []string{"localhost", "hello-guest.local"}
	if h, herr := os.Hostname(); herr == nil {
		dns = append(dns, h)
	}
	var ips []net.IP
	ifaces, _ := net.Interfaces()
	for _, iface := range ifaces {
		addrs, _ := iface.Addrs()
		for _, a := range addrs {
			if ipn, ok := a.(*net.IPNet); ok && !ipn.IP.IsLoopback() {
				ips = append(ips, ipn.IP)
			}
		}
	}

	serial, serr := rand.Int(rand.Reader, new(big.Int).Lsh(big.NewInt(1), 128))
	if serr != nil {
		return "", "", serr
	}
	tmpl := &x509.Certificate{
		SerialNumber: serial,
		Subject:      pkix.Name{CommonName: "hello-guest"},
		NotBefore:    time.Now().Add(-time.Hour),
		NotAfter:     time.Now().Add(5 * 365 * 24 * time.Hour),
		KeyUsage:     x509.KeyUsageDigitalSignature,
		ExtKeyUsage:  []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
		DNSNames:     dns,
		IPAddresses:  ips,
	}
	key, kerr := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if kerr != nil {
		return "", "", kerr
	}
	der, cerr := x509.CreateCertificate(rand.Reader, tmpl, tmpl, &key.PublicKey, key)
	if cerr != nil {
		return "", "", cerr
	}
	certOut, oerr := os.OpenFile(certFile, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0o644)
	if oerr != nil {
		return "", "", oerr
	}
	defer certOut.Close()
	if err := pem.Encode(certOut, &pem.Block{Type: "CERTIFICATE", Bytes: der}); err != nil {
		return "", "", err
	}

	keyDER, merr := x509.MarshalECPrivateKey(key)
	if merr != nil {
		return "", "", merr
	}
	keyOut, oerr := os.OpenFile(keyFile, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0o600)
	if oerr != nil {
		return "", "", oerr
	}
	defer keyOut.Close()
	if err := pem.Encode(keyOut, &pem.Block{Type: "EC PRIVATE KEY", Bytes: keyDER}); err != nil {
		return "", "", err
	}
	fmt.Fprintf(os.Stderr, "самоподписанный TLS-сертификат: %s\n", certFile)
	return certFile, keyFile, nil
}
