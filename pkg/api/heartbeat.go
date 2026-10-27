package api

// A device gone without a word (its network off, WP-B.8) was seen only by TCP's keepalive: on
// the side that accepted the session, Go's default of 15 s then nine more probes, about 2.5 min.
// Each side now pings the other every pingEvery, and a session silent for sessionSilence is
// lost (listenToSession): the user hears it, and the side that dialed dials again (redial).

import (
	"time"

	"github.com/Medboy224/medXfer/pkg/session"
)

var (
	pingEvery      = 5 * time.Second
	sessionSilence = 15 * time.Second // three pings missed
)

// pingSession pings the peer on ch until ch is closed or the server stops.
func (s *DaemonServer) pingSession(ch *session.Channel) {
	t := time.NewTicker(pingEvery)
	defer t.Stop()
	for {
		select {
		case <-s.ctx.Done():
			return
		case <-t.C:
		}
		if ch.IsClosed() || ch.Send(session.Message{Type: session.TypePing}) != nil {
			return
		}
	}
}
