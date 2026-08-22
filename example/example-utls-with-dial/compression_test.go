package main

import (
	"bytes"
	"context"
	"crypto/tls"
	"crypto/x509"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"testing"

	oohttp "github.com/BRUHItsABunny/oohttp"
	"github.com/BRUHItsABunny/oohttp/example/internal/utlsx"
)

// payload is the plaintext body every compression test expects to recover.
var payload = bytes.Repeat([]byte("the quick brown fox jumps over the lazy dog\n"), 32)

// compressingHandler returns a handler that encodes payload with the given
// list of encodings and advertises them via Content-Encoding.
func compressingHandler(t *testing.T, encodings ...string) http.Handler {
	t.Helper()
	buf := &bytes.Buffer{}
	cw := &oohttp.CompressorWriter{Writer: buf, Order: encodings}
	if _, err := cw.Write(payload); err != nil {
		t.Fatalf("CompressorWriter.Write(%v): %v", encodings, err)
	}
	if err := cw.Close(); err != nil {
		t.Fatalf("CompressorWriter.Close(%v): %v", encodings, err)
	}
	encoded := buf.Bytes()
	joined := ""
	for i, e := range encodings {
		if i > 0 {
			joined += ", "
		}
		joined += e
	}
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if joined != "" {
			w.Header().Set("Content-Encoding", joined)
		}
		w.Header().Set("Content-Type", "text/plain")
		w.Write(encoded)
	})
}

// testEnv is a running server + a client wired to reach it via the oohttp
// transport (with the utls dialer in the HTTPS/H2 case).
type testEnv struct {
	url     string
	client  *http.Client
	cleanup func()
	proto   string // for assertions: "HTTP/1.1" or "HTTP/2.0"
}

// startH1 boots an HTTP/1.1 httptest server and returns a client that uses
// this module's default utls-based transport.
func startH1(t *testing.T, h http.Handler) *testEnv {
	t.Helper()
	srv := httptest.NewServer(h)
	return &testEnv{
		url:     srv.URL,
		client:  defaultClient,
		cleanup: srv.Close,
		proto:   "HTTP/1.1",
	}
}

// startH2 boots an HTTP/2 httptest server (TLS + ALPN h2) and returns a
// client whose transport uses the utls dialer, trusts the test server's
// certificate, and negotiates ALPN h2. This exercises the oohttp HTTP/2
// stack end-to-end.
func startH2(t *testing.T, h http.Handler) *testEnv {
	t.Helper()
	srv := httptest.NewUnstartedServer(h)
	srv.EnableHTTP2 = true
	srv.StartTLS()

	pool := x509.NewCertPool()
	pool.AddCert(srv.Certificate())

	u, err := url.Parse(srv.URL)
	if err != nil {
		srv.Close()
		t.Fatalf("url.Parse: %v", err)
	}
	sni, _, err := net.SplitHostPort(u.Host)
	if err != nil {
		sni = u.Host
	}

	dialer := &utlsx.TLSDialer{
		Config: &tls.Config{
			ServerName: sni,
			RootCAs:    pool,
			NextProtos: []string{"h2", "http/1.1"},
		},
	}
	txp := utlsx.NewOOHTTPTransport(oohttp.ProxyFromEnvironment, nil, dialer.DialTLSContext)
	return &testEnv{
		url:     srv.URL,
		client:  &http.Client{Transport: txp},
		cleanup: srv.Close,
		proto:   "HTTP/2.0",
	}
}

// forEachTransport runs fn against both HTTP/1.1 and HTTP/2 servers.
func forEachTransport(t *testing.T, h http.Handler, fn func(t *testing.T, env *testEnv)) {
	t.Helper()
	for _, tc := range []struct {
		name  string
		start func(*testing.T, http.Handler) *testEnv
	}{
		{"h1", startH1},
		{"h2", startH2},
	} {
		t.Run(tc.name, func(t *testing.T) {
			env := tc.start(t, h)
			defer env.cleanup()
			fn(t, env)
		})
	}
}

// TestTransportAutoDecompressGzip verifies that when the caller does NOT
// set Accept-Encoding, the transport adds "gzip" and transparently
// decompresses the response. Runs against both HTTP/1.1 and HTTP/2.
func TestTransportAutoDecompressGzip(t *testing.T) {
	forEachTransport(t, compressingHandler(t, "gzip"), func(t *testing.T, env *testEnv) {
		resp, err := env.client.Get(env.url)
		if err != nil {
			t.Fatalf("Get: %v", err)
		}
		defer resp.Body.Close()

		if resp.Proto != env.proto {
			t.Fatalf("proto: got %q, want %q", resp.Proto, env.proto)
		}
		got, err := io.ReadAll(resp.Body)
		if err != nil {
			t.Fatalf("ReadAll: %v", err)
		}
		if !bytes.Equal(got, payload) {
			t.Fatalf("body mismatch: got %d bytes, want %d", len(got), len(payload))
		}
		if !resp.Uncompressed {
			t.Fatalf("expected resp.Uncompressed to be true")
		}
	})
}

// TestAutoDecompressCallerSetAcceptEncoding is the important one: the
// caller sets their own Accept-Encoding (typical of TLS spoofing), yet
// the transport still decodes the response transparently. Stdlib
// net/http does NOT do this; this fork does, so the body arrives as
// plaintext without the caller having to wrap it themselves.
func TestAutoDecompressCallerSetAcceptEncoding(t *testing.T) {
	for _, enc := range []string{"gzip", "deflate", "br", "zstd", "zlib"} {
		t.Run(enc, func(t *testing.T) {
			forEachTransport(t, compressingHandler(t, enc), func(t *testing.T, env *testEnv) {
				req, err := http.NewRequestWithContext(context.Background(), http.MethodGet, env.url, nil)
				if err != nil {
					t.Fatalf("NewRequest: %v", err)
				}
				req.Header.Set("Accept-Encoding", enc)

				resp, err := env.client.Do(req)
				if err != nil {
					t.Fatalf("Do: %v", err)
				}
				defer resp.Body.Close()

				if resp.Proto != env.proto {
					t.Fatalf("proto: got %q, want %q", resp.Proto, env.proto)
				}
				if !resp.Uncompressed {
					t.Fatalf("expected resp.Uncompressed=true for %s (transport should auto-decode caller-set Accept-Encoding)", enc)
				}
				if got := resp.Header.Get("Content-Encoding"); got != "" {
					t.Fatalf("Content-Encoding should be stripped after auto-decompress; got %q", got)
				}

				got, err := io.ReadAll(resp.Body)
				if err != nil {
					t.Fatalf("ReadAll: %v", err)
				}
				if !bytes.Equal(got, payload) {
					t.Fatalf("body mismatch for %s: got %d bytes, want %d", enc, len(got), len(payload))
				}
			})
		})
	}
}

// TestAutoDecompressStackedEncodings verifies a multi-layer
// Content-Encoding is unwound transparently over both HTTP/1.1 and HTTP/2.
func TestAutoDecompressStackedEncodings(t *testing.T) {
	order := []string{"br", "gzip"}
	forEachTransport(t, compressingHandler(t, order...), func(t *testing.T, env *testEnv) {
		req, err := http.NewRequestWithContext(context.Background(), http.MethodGet, env.url, nil)
		if err != nil {
			t.Fatalf("NewRequest: %v", err)
		}
		req.Header.Set("Accept-Encoding", "br, gzip")

		resp, err := env.client.Do(req)
		if err != nil {
			t.Fatalf("Do: %v", err)
		}
		defer resp.Body.Close()

		if resp.Proto != env.proto {
			t.Fatalf("proto: got %q, want %q", resp.Proto, env.proto)
		}
		if !resp.Uncompressed {
			t.Fatalf("expected resp.Uncompressed=true for stacked encoding")
		}

		got, err := io.ReadAll(resp.Body)
		if err != nil {
			t.Fatalf("ReadAll: %v", err)
		}
		if !bytes.Equal(got, payload) {
			t.Fatalf("body mismatch: got %d bytes, want %d", len(got), len(payload))
		}
	})
}

// TestDisableCompressionSkipsAutoDecode verifies that setting
// Transport.DisableCompression preserves the raw compressed body, even
// when Content-Encoding is present and supported. This is the opt-out.
func TestDisableCompressionSkipsAutoDecode(t *testing.T) {
	forEachTransport(t, compressingHandler(t, "gzip"), func(t *testing.T, env *testEnv) {
		// Reach into the transport chain and flip DisableCompression.
		var stdlib *oohttp.StdlibTransport
		switch tx := env.client.Transport.(type) {
		case *oohttp.StdlibTransport:
			stdlib = tx
		default:
			t.Skipf("client transport %T is not a *oohttp.StdlibTransport", tx)
		}
		orig := stdlib.Transport.DisableCompression
		stdlib.Transport.DisableCompression = true
		defer func() { stdlib.Transport.DisableCompression = orig }()

		req, err := http.NewRequestWithContext(context.Background(), http.MethodGet, env.url, nil)
		if err != nil {
			t.Fatalf("NewRequest: %v", err)
		}
		req.Header.Set("Accept-Encoding", "gzip")

		resp, err := env.client.Do(req)
		if err != nil {
			t.Fatalf("Do: %v", err)
		}
		defer resp.Body.Close()

		if resp.Uncompressed {
			t.Fatalf("expected resp.Uncompressed=false when DisableCompression is true")
		}
		if got := resp.Header.Get("Content-Encoding"); got != "gzip" {
			t.Fatalf("Content-Encoding should be preserved when auto-decode is disabled; got %q", got)
		}
	})
}
