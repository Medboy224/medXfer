package engine

import (
	"context"
	"net"
)

// SecureFunc turns a data connection into an authenticated, encrypted one: for example
// securechan.Server or securechan.Client bound to the key of the transfer's peer. The engine
// does not depend on the secure channel (bible §3.2, decision D5 of the J3 plan): its callers
// inject it. It runs on the connection as wrapped by the diag hooks, so faultconn still acts
// on the network bytes below TLS. On error it must close conn.
type SecureFunc func(ctx context.Context, conn net.Conn) (net.Conn, error)

// SetSecure secures every connection the Sender accepts. Without it the data travels in
// plaintext: only tests of the engine itself do that.
func (s *Sender) SetSecure(f SecureFunc) { s.secure = f }

// SetSecure secures every connection the Receiver opens.
func (r *Receiver) SetSecure(f SecureFunc) { r.secure = f }
