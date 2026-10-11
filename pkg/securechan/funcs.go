package securechan

import (
	"context"
	"net"

	"github.com/Medboy224/medXfer/pkg/identity"
)

// The functions below have the shape of engine.SecureFunc: the engine receives them by
// injection and never imports this package (bible §3.2).

// ServerFunc secures each accepted data connection with cfg; with cfg.Expected set, only the
// transfer's peer gets through.
func ServerFunc(cfg Config) func(context.Context, net.Conn) (net.Conn, error) {
	return func(ctx context.Context, c net.Conn) (net.Conn, error) {
		sc, err := Server(ctx, c, cfg)
		if err != nil {
			return nil, err
		}
		return sc, nil
	}
}

// ClientFunc secures each opened data connection with cfg.
func ClientFunc(cfg Config) func(context.Context, net.Conn) (net.Conn, error) {
	return func(ctx context.Context, c net.Conn) (net.Conn, error) {
		sc, err := Client(ctx, c, cfg)
		if err != nil {
			return nil, err
		}
		return sc, nil
	}
}

// OpenServerFunc secures the data connections of a one-shot send, whose receiver is not
// known in advance: any key is accepted and the connection is authenticated at once, so the
// data is encrypted, and onPeer reports each receiver's key (shown to the sender). The
// receiver, for its part, pins the sender's key. Mutual authentication of a one-shot send
// comes with the pairing URI and its secret (WP-3.5).
func OpenServerFunc(id *identity.Identity, onPeer func(fp identity.Fingerprint, addr net.Addr)) func(context.Context, net.Conn) (net.Conn, error) {
	cfg := Config{Identity: id, AllowUnknown: true}
	return func(ctx context.Context, c net.Conn) (net.Conn, error) {
		sc, err := Server(ctx, c, cfg)
		if err != nil {
			return nil, err
		}
		sc.Authenticate()
		if onPeer != nil {
			onPeer(sc.PeerFingerprint(), sc.RemoteAddr())
		}
		return sc, nil
	}
}
