package identity

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"sort"
	"strings"
	"sync"
	"time"
)

// trustVersion is the format version of trusted.json.
const trustVersion = 1

// Peer is a trusted device.
type Peer struct {
	Fingerprint Fingerprint `json:"-"`
	Alias       string      `json:"alias,omitempty"`
	AddedAt     time.Time   `json:"added_at"`
	// LastAddress is where the device was last seen. A different key at the address of a
	// trusted device is refused and reported (ID-05).
	LastAddress string `json:"last_address,omitempty"`
	AutoAccept  bool   `json:"auto_accept,omitempty"`
	// TagKey is the recognition key shared with this device when mutual trust was established:
	// each side announces rotating tags made with it, which only the other recognizes
	// (DISC-11). Empty for entries written before it existed: then not recognized.
	TagKey []byte `json:"tag_key,omitempty"`
}

type peerJSON struct {
	Fingerprint string `json:"fingerprint"`
	Peer
}

type trustFile struct {
	Version int        `json:"version"`
	Peers   []peerJSON `json:"peers"`
}

// TrustStore is the list of trusted devices, persisted in trusted.json with atomic writes
// (ID-04). It is safe for concurrent use within one process.
type TrustStore struct {
	path  string
	mu    sync.Mutex
	peers map[Fingerprint]Peer
}

// OpenTrustStore reads path. A missing file is an empty list.
func OpenTrustStore(path string) (*TrustStore, error) {
	t := &TrustStore{path: path, peers: map[Fingerprint]Peer{}}
	data, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return t, nil
	}
	if err != nil {
		return nil, err
	}
	var f trustFile
	if err := json.Unmarshal(data, &f); err != nil {
		return nil, fmt.Errorf("%s: %w", path, err)
	}
	if f.Version != trustVersion {
		return nil, fmt.Errorf("%s: unsupported version %d", path, f.Version)
	}
	for _, p := range f.Peers {
		fp, err := ParseFingerprint(p.Fingerprint)
		if err != nil {
			return nil, fmt.Errorf("%s: %w", path, err)
		}
		p.Peer.Fingerprint = fp
		t.peers[fp] = p.Peer
	}
	return t, nil
}

// Contains reports whether fp is trusted.
func (t *TrustStore) Contains(fp Fingerprint) bool {
	t.mu.Lock()
	defer t.mu.Unlock()
	_, ok := t.peers[fp]
	return ok
}

// Get returns the trusted device with fingerprint fp.
func (t *TrustStore) Get(fp Fingerprint) (Peer, bool) {
	t.mu.Lock()
	defer t.mu.Unlock()
	p, ok := t.peers[fp]
	return p, ok
}

// ByAddress returns the trusted device last seen at addr.
func (t *TrustStore) ByAddress(addr string) (Peer, bool) {
	t.mu.Lock()
	defer t.mu.Unlock()
	for _, p := range t.peers {
		if addr != "" && p.LastAddress == addr {
			return p, true
		}
	}
	return Peer{}, false
}

// Add trusts p (replacing an entry with the same fingerprint) and saves the list.
func (t *TrustStore) Add(p Peer) error {
	if p.Fingerprint.IsZero() {
		return errors.New("trust: empty fingerprint")
	}
	if p.AddedAt.IsZero() {
		p.AddedAt = time.Now().UTC()
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	old, had := t.peers[p.Fingerprint]
	t.peers[p.Fingerprint] = p
	if err := t.saveLocked(); err != nil {
		if had {
			t.peers[p.Fingerprint] = old
		} else {
			delete(t.peers, p.Fingerprint)
		}
		return err
	}
	return nil
}

// Forget removes the device with fingerprint fp (ID-06). It reports whether it was trusted.
func (t *TrustStore) Forget(fp Fingerprint) (bool, error) {
	t.mu.Lock()
	defer t.mu.Unlock()
	p, ok := t.peers[fp]
	if !ok {
		return false, nil
	}
	delete(t.peers, fp)
	if err := t.saveLocked(); err != nil {
		t.peers[fp] = p
		return false, err
	}
	return true, nil
}

// List returns the trusted devices, oldest first.
func (t *TrustStore) List() []Peer {
	t.mu.Lock()
	defer t.mu.Unlock()
	out := make([]Peer, 0, len(t.peers))
	for _, p := range t.peers {
		out = append(out, p)
	}
	sort.Slice(out, func(i, j int) bool {
		if !out[i].AddedAt.Equal(out[j].AddedAt) {
			return out[i].AddedAt.Before(out[j].AddedAt)
		}
		return out[i].Fingerprint.String() < out[j].Fingerprint.String()
	})
	return out
}

// Find returns the trusted device whose fingerprint starts with prefix (full or short form,
// separators ignored). The prefix must designate exactly one device.
func (t *TrustStore) Find(prefix string) (Peer, error) {
	n := normalize(prefix)
	if len(n) < 4 {
		return Peer{}, fmt.Errorf("fingerprint prefix %q is too short", prefix)
	}
	var found []Peer
	for _, p := range t.List() {
		if strings.HasPrefix(p.Fingerprint.String(), n) {
			found = append(found, p)
		}
	}
	switch len(found) {
	case 0:
		return Peer{}, fmt.Errorf("no trusted device matches %q", prefix)
	case 1:
		return found[0], nil
	}
	return Peer{}, fmt.Errorf("%q matches %d trusted devices: give more characters", prefix, len(found))
}

func (t *TrustStore) saveLocked() error {
	f := trustFile{Version: trustVersion, Peers: []peerJSON{}}
	for _, p := range t.peers {
		f.Peers = append(f.Peers, peerJSON{Fingerprint: p.Fingerprint.String(), Peer: p})
	}
	sort.Slice(f.Peers, func(i, j int) bool { return f.Peers[i].Fingerprint < f.Peers[j].Fingerprint })
	data, err := json.MarshalIndent(f, "", "  ")
	if err != nil {
		return err
	}
	return writeFileAtomic(t.path, append(data, '\n'), 0o600)
}
