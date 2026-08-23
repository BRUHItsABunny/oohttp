// Copyright 2026 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package http2_test

import (
	"testing"
	"testing/synctest"

	http "github.com/BRUHItsABunny/oohttp"

	. "github.com/BRUHItsABunny/oohttp/internal/http2"
)

// These tests cover the github.com/BRUHItsABunny/oohttp extensions that let a
// caller control the exact shape of the frames sent when a connection opens,
// which is what makes this fork's HTTP/2 fingerprint match a real browser's.

func TestOOHTTPCustomConnectionPreface(t *testing.T) {
	synctest.Test(t, testOOHTTPCustomConnectionPreface)
}
func testOOHTTPCustomConnectionPreface(t *testing.T) {
	tc := newTestClientConn(t, func(tr1 *http.Transport) {
		tr1.HasCustomInitialSettings = true
		// Index+1 is the SETTINGS ID; -1 means "do not send this one".
		// This is Chrome's SETTINGS frame.
		tr1.HTTP2SettingsFrameParameters = []int64{
			65536,   // 1: HEADER_TABLE_SIZE
			-1,      // 2: ENABLE_PUSH (not sent)
			1000,    // 3: MAX_CONCURRENT_STREAMS
			6291456, // 4: INITIAL_WINDOW_SIZE
			-1,      // 5: MAX_FRAME_SIZE (not sent)
			262144,  // 6: MAX_HEADER_LIST_SIZE
		}
		tr1.HasCustomWindowUpdate = true
		tr1.WindowUpdateIncrement = 15663105
		tr1.HTTP2PriorityFrameSettings = &http.HTTP2PriorityFrameSettings{
			HeaderFrame: &http.HTTP2Priority{StreamDep: 13, Exclusive: true, Weight: 41},
			// The slice index is the stream ID of each PRIORITY frame.
			PriorityFrames: []*http.HTTP2Priority{
				3: {StreamDep: 0, Exclusive: false, Weight: 200},
				5: {StreamDep: 0, Exclusive: false, Weight: 100},
				7: {StreamDep: 0, Exclusive: false, Weight: 0},
			},
		}
	})

	tc.wantSettings(map[SettingID]uint32{
		SettingHeaderTableSize:      65536,
		SettingMaxConcurrentStreams: 1000,
		SettingInitialWindowSize:    6291456,
		SettingMaxHeaderListSize:    262144,
	})
	tc.wantWindowUpdate(0, 15663105)

	for _, want := range []struct {
		streamID uint32
		weight   uint8
	}{{3, 200}, {5, 100}, {7, 0}} {
		fr := readFrame[*PriorityFrame](t, &tc.testConnFramer)
		if fr.StreamID != want.streamID || fr.Weight != want.weight {
			t.Fatalf("got PRIORITY stream=%v weight=%v, want stream=%v weight=%v",
				fr.StreamID, fr.Weight, want.streamID, want.weight)
		}
	}

	// Stream IDs consumed by the PRIORITY frames must not be reused, and the
	// HEADERS frame must carry the configured priority.
	tc.writeSettings()
	tc.writeSettingsAck()
	tc.wantSettingsAck()

	req, _ := http.NewRequest("GET", "https://dummy.tld/", nil)
	tc.roundTrip(req)

	hf := readFrame[*HeadersFrame](t, &tc.testConnFramer)
	if hf.StreamID <= 7 {
		t.Errorf("HEADERS used stream %v, want a stream above the PRIORITY frames", hf.StreamID)
	}
	if !hf.HasPriority() {
		t.Fatalf("HEADERS frame has no priority, want one")
	}
	if got := hf.Priority; got.StreamDep != 13 || !got.Exclusive || got.Weight != 41 {
		t.Errorf("HEADERS priority = %+v, want {StreamDep:13 Exclusive:true Weight:41}", got)
	}
}

// Without the extensions the default (upstream) preface must be unchanged.
func TestOOHTTPDefaultConnectionPreface(t *testing.T) {
	synctest.Test(t, testOOHTTPDefaultConnectionPreface)
}
func testOOHTTPDefaultConnectionPreface(t *testing.T) {
	tc := newTestClientConn(t)
	tc.wantSettings(map[SettingID]uint32{
		SettingEnablePush: 0,
	})
	tc.wantWindowUpdate(0, uint32(TransportDefaultConnFlow))

	req, _ := http.NewRequest("GET", "https://dummy.tld/", nil)
	tc.writeSettings()
	tc.writeSettingsAck()
	tc.wantSettingsAck()
	tc.roundTrip(req)
	hf := readFrame[*HeadersFrame](t, &tc.testConnFramer)
	if hf.HasPriority() {
		t.Errorf("HEADERS frame has priority %+v, want none", hf.Priority)
	}
}
