// Copyright 2026 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

// Tests for the github.com/BRUHItsABunny/oohttp extensions that must keep
// working the same way over HTTP/1 and HTTP/2.

package http_test

import (
	"bytes"
	"compress/gzip"
	"io"
	"slices"
	"strings"
	"testing"

	. "github.com/BRUHItsABunny/oohttp"
)

// A client that spoofs a browser sets its own Accept-Encoding, and still
// expects the body to be decoded for it. (The stdlib only decodes responses
// to requests where it added Accept-Encoding itself.)
func TestTransportDecompressesCallerRequestedEncoding(t *testing.T) {
	run(t, testTransportDecompressesCallerRequestedEncoding)
}
func testTransportDecompressesCallerRequestedEncoding(t *testing.T, mode testMode) {
	const body = "hello, decompressed world"
	var gzipped bytes.Buffer
	zw := gzip.NewWriter(&gzipped)
	io.WriteString(zw, body)
	zw.Close()

	cst := newClientServerTest(t, mode, HandlerFunc(func(w ResponseWriter, r *Request) {
		if got := r.Header.Get("Accept-Encoding"); got != "gzip, deflate, br" {
			t.Errorf("server saw Accept-Encoding %q, want the value the caller set", got)
		}
		w.Header().Set("Content-Encoding", "gzip")
		w.Write(gzipped.Bytes())
	}))

	req, _ := NewRequest("GET", cst.ts.URL, nil)
	req.Header.Set("Accept-Encoding", "gzip, deflate, br")
	res, err := cst.c.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer res.Body.Close()
	got, err := io.ReadAll(res.Body)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != body {
		t.Errorf("body = %q, want %q", got, body)
	}
	if !res.Uncompressed {
		t.Errorf("Response.Uncompressed = false, want true")
	}
	if ce := res.Header.Get("Content-Encoding"); ce != "" {
		t.Errorf("Content-Encoding = %q, want it to have been removed", ce)
	}
}

// An unknown Content-Encoding must be handed to the caller untouched.
func TestTransportLeavesUnknownEncodingAlone(t *testing.T) {
	run(t, testTransportLeavesUnknownEncodingAlone)
}
func testTransportLeavesUnknownEncodingAlone(t *testing.T, mode testMode) {
	cst := newClientServerTest(t, mode, HandlerFunc(func(w ResponseWriter, r *Request) {
		w.Header().Set("Content-Encoding", "banana")
		io.WriteString(w, "raw")
	}))

	req, _ := NewRequest("GET", cst.ts.URL, nil)
	req.Header.Set("Accept-Encoding", "banana")
	res, err := cst.c.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer res.Body.Close()
	got, _ := io.ReadAll(res.Body)
	if string(got) != "raw" {
		t.Errorf("body = %q, want %q", got, "raw")
	}
	if res.Uncompressed {
		t.Errorf("Response.Uncompressed = true, want false")
	}
	if ce := res.Header.Get("Content-Encoding"); ce != "banana" {
		t.Errorf("Content-Encoding = %q, want %q", ce, "banana")
	}
}

// Transport.TrackResponseHeaderOrder records the order the server sent its
// headers in, which is a server fingerprint.
func TestTransportTrackResponseHeaderOrder(t *testing.T) {
	run(t, testTransportTrackResponseHeaderOrder)
}
func testTransportTrackResponseHeaderOrder(t *testing.T, mode testMode) {
	cst := newClientServerTest(t, mode, HandlerFunc(func(w ResponseWriter, r *Request) {
		w.Header().Set("X-Zebra", "1")
		w.Header().Set("X-Alpha", "2")
		w.Header().Set("X-Mike", "3")
		io.WriteString(w, "ok")
	}), func(tr *Transport) {
		tr.TrackResponseHeaderOrder = true
	})

	res, err := cst.c.Get(cst.ts.URL)
	if err != nil {
		t.Fatal(err)
	}
	defer res.Body.Close()
	io.Copy(io.Discard, res.Body)

	order, ok := res.Header[HeaderOrderKey]
	if !ok {
		t.Fatalf("response has no %v, want one", HeaderOrderKey)
	}
	// Every header we received, except the order key itself, must appear
	// exactly once in the recorded order.
	var want []string
	for k := range res.Header {
		if k == HeaderOrderKey {
			continue
		}
		want = append(want, strings.ToLower(k))
	}
	var got []string
	for _, k := range order {
		got = append(got, strings.ToLower(k))
	}
	slices.Sort(want)
	slices.Sort(got)
	if !slices.Equal(got, want) {
		t.Errorf("%v = %q, want the received header names %q", HeaderOrderKey, got, want)
	}
	if i, j := slices.Index(got, "x-alpha"), slices.Index(got, "x-zebra"); i < 0 || j < 0 {
		t.Errorf("%v = %q, missing the handler's headers", HeaderOrderKey, order)
	}
}

// Without TrackResponseHeaderOrder the magic key must not leak into responses.
func TestTransportNoHeaderOrderByDefault(t *testing.T) {
	run(t, testTransportNoHeaderOrderByDefault)
}
func testTransportNoHeaderOrderByDefault(t *testing.T, mode testMode) {
	cst := newClientServerTest(t, mode, HandlerFunc(func(w ResponseWriter, r *Request) {
		io.WriteString(w, "ok")
	}))
	res, err := cst.c.Get(cst.ts.URL)
	if err != nil {
		t.Fatal(err)
	}
	defer res.Body.Close()
	io.Copy(io.Discard, res.Body)
	if v, ok := res.Header[HeaderOrderKey]; ok {
		t.Errorf("response has %v = %q, want none", HeaderOrderKey, v)
	}
}
