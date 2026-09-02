package main

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"fmt"
	"math/big"
	"net"
	"net/http"
	"time"
)

/*
Transport security and response headers.

TLS is not optional for anything that carries a token or a password: on plain
HTTP both are readable by every device on the path, and a stolen bearer token
is a stolen account.

In production TLS is usually terminated by a load balancer or ingress, and
this process speaks HTTP behind it. That is fine - as long as the hop behind
the terminator is a private network, and the service refuses to issue or
accept credentials when it thinks the connection was plaintext.
*/

// HardenedTLSConfig is what to hand http.Server when this process terminates
// TLS itself.
func HardenedTLSConfig() *tls.Config {
	return &tls.Config{
		// TLS 1.0 and 1.1 are deprecated and broken; 1.2 is the floor, 1.3 is
		// preferred and negotiated automatically.
		MinVersion: tls.VersionTLS12,

		// Only forward-secret AEAD suites. A recorded session must not become
		// readable later if the server key leaks.
		CipherSuites: []uint16{
			tls.TLS_ECDHE_ECDSA_WITH_AES_128_GCM_SHA256,
			tls.TLS_ECDHE_RSA_WITH_AES_128_GCM_SHA256,
			tls.TLS_ECDHE_ECDSA_WITH_AES_256_GCM_SHA384,
			tls.TLS_ECDHE_RSA_WITH_AES_256_GCM_SHA384,
			tls.TLS_ECDHE_ECDSA_WITH_CHACHA20_POLY1305,
			tls.TLS_ECDHE_RSA_WITH_CHACHA20_POLY1305,
		},

		CurvePreferences: []tls.CurveID{tls.X25519, tls.CurveP256},
	}
}

// SecurityHeaders sets the headers every HTML- or JSON-serving service should
// send. Each one closes a specific hole.
func SecurityHeaders(httpsOnly bool) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			header := w.Header()

			// Never let a browser guess a JSON response is HTML and run it.
			header.Set("X-Content-Type-Options", "nosniff")

			// Legacy clickjacking defence; CSP frame-ancestors is the modern
			// one, and both are cheap.
			header.Set("X-Frame-Options", "DENY")

			// An API returns no HTML, so the strictest policy is also the
			// correct one.
			header.Set("Content-Security-Policy", "default-src 'none'; frame-ancestors 'none'")

			// Do not leak internal URLs to third parties through Referer.
			header.Set("Referrer-Policy", "no-referrer")

			// Turn off browser features this API never uses.
			header.Set("Permissions-Policy", "geolocation=(), camera=(), microphone=()")

			// Responses carrying user data must not be cached by proxies.
			header.Set("Cache-Control", "no-store")

			if httpsOnly {
				// HSTS: after the first visit, the browser refuses plaintext
				// for this host. Only send it over HTTPS - sending it from a
				// plaintext dev server locks developers out of their laptop.
				header.Set("Strict-Transport-Security", "max-age=31536000; includeSubDomains")
			}

			next.ServeHTTP(w, r)
		})
	}
}

// RequireHTTPS redirects plaintext requests, or refuses them outright for
// non-idempotent methods where a redirect would leak the body.
//
// r.TLS is nil behind a terminating proxy, so the forwarded scheme header is
// honoured only when the deployment says the proxy is trusted.
func RequireHTTPS(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		secure := r.TLS != nil

		if !secure && trustProxyHeaders && r.Header.Get("X-Forwarded-Proto") == "https" {
			secure = true
		}

		if secure || allowPlaintext {
			next.ServeHTTP(w, r)
			return
		}

		if r.Method != http.MethodGet && r.Method != http.MethodHead {
			// A POST redirect would have the client re-send credentials, and
			// the first plaintext request already exposed them.
			writeError(w, http.StatusUpgradeRequired, "https is required")
			return
		}

		target := "https://" + r.Host + r.URL.RequestURI()

		http.Redirect(w, r, target, http.StatusPermanentRedirect)
	})
}

// SelfSignedCertificate generates a throwaway certificate so `go run . tls`
// works with no setup. Browsers will warn, which is correct: the certificate
// proves nothing. Production certificates come from a CA (Let's Encrypt via
// golang.org/x/crypto/acme/autocert, or the platform's ingress).
func SelfSignedCertificate(host string) (tls.Certificate, error) {
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return tls.Certificate{}, fmt.Errorf("generate key: %w", err)
	}

	serialLimit := new(big.Int).Lsh(big.NewInt(1), 128)

	serial, err := rand.Int(rand.Reader, serialLimit)
	if err != nil {
		return tls.Certificate{}, fmt.Errorf("generate serial: %w", err)
	}

	template := x509.Certificate{
		SerialNumber:          serial,
		Subject:               pkix.Name{Organization: []string{"OneHundredDay Day 54 (development only)"}},
		NotBefore:             time.Now().Add(-time.Hour),
		NotAfter:              time.Now().Add(24 * time.Hour),
		KeyUsage:              x509.KeyUsageDigitalSignature | x509.KeyUsageCertSign,
		ExtKeyUsage:           []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
		DNSNames:              []string{host},
		IPAddresses:           []net.IP{net.ParseIP("127.0.0.1"), net.IPv6loopback},
		IsCA:                  true,
		BasicConstraintsValid: true,
	}

	der, err := x509.CreateCertificate(rand.Reader, &template, &template, &key.PublicKey, key)
	if err != nil {
		return tls.Certificate{}, fmt.Errorf("create certificate: %w", err)
	}

	return tls.Certificate{
		Certificate: [][]byte{der},
		PrivateKey:  key,
	}, nil
}
