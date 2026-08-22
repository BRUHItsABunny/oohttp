package ja3x

import (
	"net"
	"sync"
)

// newConnWrapper creates a new [connWrapper].
func newConnWrapper(conn net.Conn) *connWrapper {
	return &connWrapper{
		Conn:  conn,
		hello: []byte{},
		mu:    sync.Mutex{},
	}
}

// connWrapper is a wrapper that extracts the raw TLS client hello.
type connWrapper struct {
	// Conn is the underlying conn.
	net.Conn

	// hello contains the client hello.
	hello []byte

	// helloDone indicates we captured the whole client hello record.
	helloDone bool

	// mu provides mutual exclusion.
	mu sync.Mutex
}

// tlsRecordHeaderLen is the size of a TLS record header.
const tlsRecordHeaderLen = 5

// helloComplete tells whether hello contains a whole TLS record. We cannot
// assume the client hello arrives in a single Read: crypto/tls sizes its
// initial read buffer conservatively, so a large client hello (e.g. one
// carrying a post-quantum key share) is delivered in several chunks.
func helloComplete(hello []byte) bool {
	if len(hello) < tlsRecordHeaderLen {
		return false
	}
	length := int(hello[3])<<8 | int(hello[4])
	return len(hello) >= tlsRecordHeaderLen+length
}

// Hello returns a copy of the bytes inside the client hello.
func (cw *connWrapper) Hello() (out []byte) {
	defer cw.mu.Unlock()
	cw.mu.Lock()
	hello := cw.hello
	if helloComplete(hello) {
		// Drop any byte read past the client hello record.
		hello = hello[:tlsRecordHeaderLen+(int(hello[3])<<8|int(hello[4]))]
	}
	out = append(out, hello...)
	return
}

// Read implements net.Conn.
func (cw *connWrapper) Read(data []byte) (int, error) {
	count, err := cw.Conn.Read(data)
	if err != nil {
		return 0, err
	}
	defer cw.mu.Unlock()
	cw.mu.Lock()
	if !cw.helloDone {
		cw.hello = append(cw.hello, data[:count]...) // makes a copy
		cw.helloDone = helloComplete(cw.hello)
	}
	return count, nil
}
