package api

// A device gone without a word (its network off, WP-B.8) was seen only by TCP's keepalive: on
// the side that accepted the session, Go's default of 15 s then nine more probes, about 2.5 min.
// Each side now pings the other every pingEvery, and a session silent for sessionSilence is
// lost (listenToSession): the user hears it, and the side that dialed dials again (redial).

import (
	"sync/atomic"
	"time"

	"github.com/Medboy224/medXfer/pkg/session"
)

var (
	pingEvery      = newDuration(5 * time.Second)
	sessionSilence = newDuration(15 * time.Second) // three pings missed
)

// duration is a parameter read by the sessions' goroutines while a test may change it: the
// sessions of an earlier test can still run (a race the detector found, CI of #242).
type duration struct{ v atomic.Int64 }

func newDuration(d time.Duration) *duration {
	var x duration
	x.set(d)
	return &x
}

func (d *duration) get() time.Duration  { return time.Duration(d.v.Load()) }
func (d *duration) set(v time.Duration) { d.v.Store(int64(v)) }

// pingSession pings the peer on ch until ch is closed or the server stops.
func (s *DaemonServer) pingSession(ch *session.Channel) {
	t := time.NewTicker(pingEvery.get())
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
