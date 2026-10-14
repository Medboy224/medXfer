package pairing

import (
	"bytes"
	"context"
	"net"
	"testing"
	"time"

	"github.com/Medboy224/medXfer/pkg/protocol"
	"github.com/Medboy224/medXfer/pkg/securechan"
	"github.com/Medboy224/medXfer/pkg/session"
)

// tlsPair opens one secure connection on ln, both ends in state Unauth, without the Gate (the
// fuzzer connects far more often than LIM-01 allows).
func tlsPair(t *testing.T, ln net.Listener, server, client Options) (*securechan.Conn, *securechan.Conn) {
	t.Helper()
	srv := make(chan *securechan.Conn, 1)
	go func() {
		raw, err := ln.Accept()
		if err != nil {
			srv <- nil
			return
		}
		c, _ := securechan.Server(context.Background(), raw, securechan.Config{Identity: server.Identity, AllowUnknown: true})
		srv <- c
	}()
	c, err := securechan.Dial(context.Background(), ln.Addr().String(), securechan.Config{Identity: client.Identity, AllowUnknown: true})
	if err != nil {
		t.Fatal(err)
	}
	s := <-srv
	if s == nil {
		t.Fatal("server handshake failed")
	}
	return c, s
}

// WP-5.5: whatever a peer sends in pair_pin (suite, share) and in the frame that should be
// pair_pin_confirm, RespondPIN never panics and never pairs: only the PIN's holder can
// confirm. The PIN keeps its attempts, so the counted path is fuzzed (and the decoy path once
// they run out or the PIN expires).
func FuzzRespondPIN(f *testing.F) {
	responder, initiator := device(f, "pc"), device(f, "phone")
	pin, err := NewPIN(PINConfig{Attempts: 1 << 30, TTL: time.Hour})
	if err != nil {
		f.Fatal(err)
	}
	f.Cleanup(pin.Close)
	ln, err := net.Listen("tcp4", "127.0.0.1:0")
	if err != nil {
		f.Fatal(err)
	}
	f.Cleanup(func() { ln.Close() })

	f.Add(PINSuite, make([]byte, 32), []byte(`{"type":"pair_pin_confirm","confirm":"AAAA","device_name":"x"}`))
	f.Add(PINSuite, bytes.Repeat([]byte{0xff}, 32), []byte(`not json`))
	f.Add("", []byte{}, []byte(`{}`))
	f.Add(PINSuite, []byte{1}, []byte(`{"type":"pair_pin_confirm","confirm":null}`))
	f.Add("CPACE-RISTR255-SHA512\x00", make([]byte, 64), []byte(`{"type":"pair_accept"}`))
	f.Fuzz(func(t *testing.T, suite string, share, confirm []byte) {
		client, server := tlsPair(t, ln, responder, initiator)
		defer client.Close()
		defer server.Close()
		done := make(chan error, 1)
		go func() {
			msg := session.Message{Type: TypePIN, Suite: suite, Share: share}
			_, err := RespondPIN(session.NewChannel(server), server, msg, pin, responder.Identity.Fingerprint)
			server.Close() // as Serve does after a refusal: the client's read ends
			done <- err
		}()
		ch := session.NewChannel(client)
		_ = client.SetDeadline(time.Now().Add(5 * time.Second))
		if m, err := ch.Read(); err == nil && m.Type == TypePINReply {
			_, _ = client.Write(protocol.AppendFrame(nil, protocol.TypeControl, confirm))
		}
		select {
		case err := <-done:
			if err == nil {
				t.Fatalf("paired without the PIN: suite %q, share %x, confirm %q", suite, share, confirm)
			}
		case <-time.After(15 * time.Second):
			t.Fatal("RespondPIN did not return")
		}
	})
}
