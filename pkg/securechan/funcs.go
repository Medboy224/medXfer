package securechan

import (
	"context"
	"net"
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
