package api

// Discovery on demand (WP-6.5b of the J6a plan): a device announces itself only when its user
// asked (DISC-05), and the platform may give the network interfaces Android hides (DISC-10).

import (
	"context"
	"encoding/json"
	"time"

	"github.com/Medboy224/medXfer/pkg/discovery"
	"github.com/Medboy224/medXfer/pkg/pairing"
)

// SetDiscoverable starts or stops announcing this device (mDNS, fallback beacon) and answering
// discovery probes. The dashboard's daemon is discoverable while it serves; pkg/core starts
// silent, until the interface asks (set_discoverable), for instance while it receives.
func (s *DaemonServer) SetDiscoverable(on bool) {
	s.mu.Lock()
	disc := s.discSrv
	if disc == nil || on == (s.discCancel != nil) {
		s.mu.Unlock()
		return
	}
	if !on {
		s.discCancel()
		s.discCancel = nil
		s.mu.Unlock()
		return
	}
	ctx, cancel := context.WithCancel(s.ctx)
	s.discCancel = cancel
	s.mu.Unlock()
	disc.Start(ctx) // outside the lock: finding the address may take a while (ifconfig on Termux)
}

// Discoverable reports whether this device announces itself now.
func (s *DaemonServer) Discoverable() bool {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.discCancel != nil
}

// instance is the generic name this device announces (DISC-06): shown beside the PIN and the
// QR code, so that the other device picks the right one in its list (DISC-07).
func (s *DaemonServer) instance() string {
	if s.discSrv == nil {
		return ""
	}
	return s.discSrv.GetPeer().DeviceName
}

// SetDiscoverablePayload is the payload of set_discoverable.
type SetDiscoverablePayload struct {
	On bool `json:"on"`
}

func (s *DaemonServer) handleSetDiscoverable(rs Responder, req RequestMessage) {
	var p SetDiscoverablePayload
	if err := json.Unmarshal(req.Payload, &p); err != nil {
		rs.Reply(NewEvent("action_error", map[string]string{"error": "invalid set_discoverable payload"}, req.ID))
		return
	}
	s.SetDiscoverable(p.On)
	rs.Reply(NewEvent("status", s.getStatus(), req.ID))
}

// SetNetworkInfoPayload is the payload of set_network_info: the interfaces the platform sees.
type SetNetworkInfoPayload struct {
	Interfaces []discovery.NetworkInterface `json:"interfaces"`
}

func (s *DaemonServer) handleSetNetworkInfo(rs Responder, req RequestMessage) {
	var p SetNetworkInfoPayload
	if err := json.Unmarshal(req.Payload, &p); err != nil || p.Interfaces == nil {
		rs.Reply(NewEvent("action_error", map[string]string{"error": "invalid set_network_info payload"}, req.ID))
		return
	}
	if err := discovery.SetNetworkInfo(p.Interfaces); err != nil {
		rs.Reply(NewEvent("action_error", map[string]string{"error": err.Error()}, req.ID))
		return
	}
	if s.Discoverable() { // announce the new address, from the new interface
		s.SetDiscoverable(false)
		s.SetDiscoverable(true)
	}
	rs.Reply(NewEvent("status", s.getStatus(), req.ID))
}

// scannedPeer is a device of a scan, with the name this device recorded for it when it is a
// device of mutual trust: recognized by its tags (DISC-11), never by its address, which another
// device may take. The name never goes out (DISC-06); the tags mean nothing to the interface.
type scannedPeer struct {
	discovery.Peer
	TrustedAlias string   `json:"trusted_alias,omitempty"`
	Tags         []string `json:"-"`
}

func (s *DaemonServer) recognize(peers []discovery.Peer) []scannedPeer {
	s.mu.RLock()
	trust := s.trust
	s.mu.RUnlock()
	now := time.Now()
	out := make([]scannedPeer, 0, len(peers))
	for _, p := range peers {
		sp := scannedPeer{Peer: p}
		if known, ok := pairing.Recognize(trust, p.Tags, now); ok {
			sp.TrustedAlias = known.Alias
			if sp.TrustedAlias == "" {
				sp.TrustedAlias = known.Fingerprint.Short()
			}
		}
		out = append(out, sp)
	}
	return out
}

// announceTags are the recognition tags this device announces (DISC-11): one per device of
// mutual trust, read at each announcement.
func (s *DaemonServer) announceTags() []string {
	s.mu.RLock()
	trust, id := s.trust, s.identity
	s.mu.RUnlock()
	if id == nil {
		return nil
	}
	return pairing.AnnounceTags(trust, id.Fingerprint, time.Now())
}
