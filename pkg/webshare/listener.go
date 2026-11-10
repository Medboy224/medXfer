package webshare

import (
	"bufio"
	"bytes"
	"fmt"
	"io"
	"net"
	"net/http"
	"sync"
	"time"
)

// sniffing is the session's port as the guest's browser reaches it. A TLS connection goes on to
// the HTTPS server. A guest who typed the address without "https://" arrives in plain HTTP: it
// is answered with a redirect to the page in HTTPS, never with Go's « Client sent an HTTP
// request to an HTTPS server » (real test of 10 November 2026, Edge on the PC).
type sniffing struct {
	net.Listener
	target string // https://<addr>:<port>/s
	conns  chan net.Conn
	done   chan struct{}
	once   sync.Once
}

func newSniffing(ln net.Listener, target string) *sniffing {
	l := &sniffing{Listener: ln, target: target, conns: make(chan net.Conn), done: make(chan struct{})}
	go l.loop()
	return l
}

func (l *sniffing) loop() {
	for {
		c, err := l.Listener.Accept()
		if err != nil {
			_ = l.Close()
			return
		}
		go l.sniff(c) // never the next Accept waiting on a slow client
	}
}

// sniff reads the connection's first byte: 0x16 opens a TLS handshake.
func (l *sniffing) sniff(c net.Conn) {
	_ = c.SetReadDeadline(time.Now().Add(10 * time.Second))
	var first [1]byte
	if _, err := io.ReadFull(c, first[:]); err != nil {
		_ = c.Close()
		return
	}
	_ = c.SetReadDeadline(time.Time{})
	if first[0] == 0x16 {
		select {
		case l.conns <- &peeked{Conn: c, first: first[0]}:
		case <-l.done:
			_ = c.Close()
		}
		return
	}
	// Plain HTTP: its request head read, bounded (WEB-08), then the redirect.
	_ = c.SetDeadline(time.Now().Add(5 * time.Second))
	br := bufio.NewReader(io.MultiReader(bytes.NewReader(first[:]), io.LimitReader(c, 16<<10)))
	if _, err := http.ReadRequest(br); err == nil {
		_, _ = fmt.Fprintf(c, "HTTP/1.1 301 Moved Permanently\r\nLocation: %s\r\nReferrer-Policy: no-referrer\r\nContent-Length: 0\r\nConnection: close\r\n\r\n", l.target)
	}
	_ = c.Close()
}

func (l *sniffing) Accept() (net.Conn, error) {
	select {
	case c := <-l.conns:
		return c, nil
	case <-l.done:
		return nil, net.ErrClosed
	}
}

func (l *sniffing) Close() error {
	l.once.Do(func() { close(l.done) })
	return l.Listener.Close()
}

// peeked gives back the byte sniff read before the rest.
type peeked struct {
	net.Conn
	first byte
	given bool
}

func (c *peeked) Read(p []byte) (int, error) {
	if !c.given && len(p) > 0 {
		c.given = true
		p[0] = c.first
		return 1, nil
	}
	return c.Conn.Read(p)
}
