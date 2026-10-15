package api

// Discovery on demand (WP-6.5b of the J6a plan): a device announces itself only when its user
// asked (DISC-05), and the platform may give the network interfaces Android hides (DISC-10).

import (
	"context"
	"encoding/json"

	"github.com/Medboy224/medXfer/pkg/discovery"
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

// scannedPeer is a device of a scan, with the alias of the trusted device seen at its address:
// recognized in the list before any pairing, locally only (the alias never goes out).
type scannedPeer struct {
	discovery.Peer
	TrustedAlias string `json:"trusted_alias,omitempty"`
}

func (s *DaemonServer) recognize(peers []discovery.Peer) []scannedPeer {
	s.mu.RLock()
	trust := s.trust
	s.mu.RUnlock()
	out := make([]scannedPeer, 0, len(peers))
	for _, p := range peers {
		sp := scannedPeer{Peer: p}
		if trust != nil {
			if known, ok := trust.ByAddress(p.HostIP); ok {
				sp.TrustedAlias = known.Alias
			}
		}
		out = append(out, sp)
	}
	return out
}
