package identity

import (
	"encoding/json"
	"errors"
	"fmt"
	"maps"
	"os"
	"path/filepath"
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
	// Renamed: the user named this device (xfer trust rename); the name it sends no longer
	// replaces the alias.
	Renamed bool `json:"renamed,omitempty"`
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
// (ID-04). Several processes may share the file, the CLI beside a node or the app (#303): each
// change is made on the file as it is now, under a lock (trusted.json.lock), and a read takes
// the file again when another process wrote it. It is safe for concurrent use.
type TrustStore struct {
	path  string
	mu    sync.Mutex
	peers map[Fingerprint]Peer
	stamp fileStamp // trusted.json as last read or written here
	// forgotten: devices forgotten here whose write failed. They stay out of the list, and the
	// next write writes them out (ID-06).
	forgotten map[Fingerprint]bool
}

// fileStamp tells whether a file changed since it was read. An atomic write makes a new file:
// its identity (os.SameFile) tells it even with the same size and date.
type fileStamp struct {
	info os.FileInfo // nil: no file
	mod  time.Time
	size int64
}

func stampOf(path string) fileStamp {
	fi, err := os.Stat(path)
	if err != nil {
		return fileStamp{}
	}
	os.SameFile(fi, fi) // reads the file's identity now: Windows reads it at the first use
	return fileStamp{info: fi, mod: fi.ModTime(), size: fi.Size()}
}

func (a fileStamp) same(b fileStamp) bool {
	if a.info == nil || b.info == nil {
		return a.info == b.info
	}
	return os.SameFile(a.info, b.info) && a.size == b.size && a.mod.Equal(b.mod)
}

// OpenTrustStore reads path. A missing file is an empty list.
func OpenTrustStore(path string) (*TrustStore, error) {
	peers, stamp, err := readTrust(path)
	if err != nil {
		return nil, err
	}
	return &TrustStore{path: path, peers: peers, stamp: stamp, forgotten: map[Fingerprint]bool{}}, nil
}

// readTrust reads the list in path, and its stamp, taken before: a write in between is seen at
// the next read. A missing file is an empty list.
func readTrust(path string) (map[Fingerprint]Peer, fileStamp, error) {
	peers := map[Fingerprint]Peer{}
	stamp := stampOf(path)
	data, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return peers, fileStamp{}, nil
	}
	if err != nil {
		return nil, stamp, err
	}
	var f trustFile
	if err := json.Unmarshal(data, &f); err != nil {
		return nil, stamp, fmt.Errorf("%s: %w", path, err)
	}
	if f.Version != trustVersion {
		return nil, stamp, fmt.Errorf("%s: unsupported version %d", path, f.Version)
	}
	for _, p := range f.Peers {
		fp, err := ParseFingerprint(p.Fingerprint)
		if err != nil {
			return nil, stamp, fmt.Errorf("%s: %w", path, err)
		}
		p.Peer.Fingerprint = fp
		peers[fp] = p.Peer
	}
	return peers, stamp, nil
}

// refreshLocked takes the list from the file again when another process wrote it. A file that
// cannot be read leaves the list as it was.
func (t *TrustStore) refreshLocked() {
	if stampOf(t.path).same(t.stamp) {
		return
	}
	if peers, stamp, err := readTrust(t.path); err == nil {
		t.withoutForgotten(peers)
		t.peers, t.stamp = peers, stamp
	}
}

// withoutForgotten takes out of peers the devices forgotten here and not written yet; it
// reports whether there were any.
func (t *TrustStore) withoutForgotten(peers map[Fingerprint]Peer) bool {
	found := false
	for fp := range t.forgotten {
		if _, ok := peers[fp]; ok {
			delete(peers, fp)
			found = true
		}
	}
	return found
}

// change applies apply to the list as the file holds it now, under the file's lock, and saves
// the list when apply reports a change. A change another process made is never lost (#303).
func (t *TrustStore) change(apply func(peers map[Fingerprint]Peer) bool) error {
	t.mu.Lock()
	defer t.mu.Unlock()
	unlock, err := lockTrust(t.path)
	if err != nil {
		return err
	}
	defer unlock()
	peers, stamp, err := readTrust(t.path)
	if err != nil {
		return err
	}
	unwritten := t.withoutForgotten(peers)
	t.peers, t.stamp = peers, stamp
	next := maps.Clone(peers)
	changed := apply(next)
	for fp := range t.forgotten {
		if _, again := next[fp]; again { // trusted again since
			delete(t.forgotten, fp)
		}
	}
	if !changed && !unwritten {
		return nil
	}
	if err := saveTrust(t.path, next); err != nil {
		return err
	}
	t.peers, t.stamp = next, stampOf(t.path)
	clear(t.forgotten)
	return nil
}

// lockTrust takes the lock that the processes writing the list in path share; the function it
// returns releases it.
func lockTrust(path string) (func(), error) {
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return nil, err
	}
	f, err := os.OpenFile(path+".lock", os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		return nil, err
	}
	if err := lockFile(f); err != nil {
		f.Close()
		return nil, err
	}
	return func() { _ = unlockFile(f); _ = f.Close() }, nil
}

// Contains reports whether fp is trusted.
func (t *TrustStore) Contains(fp Fingerprint) bool {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.refreshLocked()
	_, ok := t.peers[fp]
	return ok
}

// Get returns the trusted device with fingerprint fp.
func (t *TrustStore) Get(fp Fingerprint) (Peer, bool) {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.refreshLocked()
	p, ok := t.peers[fp]
	return p, ok
}

// ByAddress returns the trusted device last seen at addr.
func (t *TrustStore) ByAddress(addr string) (Peer, bool) {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.refreshLocked()
	for _, p := range t.peers {
		if addr != "" && p.LastAddress == addr {
			return p, true
		}
	}
	return Peer{}, false
}

// KeyChangedAt implements ID-05: it returns the trusted device last seen at addr when the
// key fp shown there now is not trusted. A trusted key is no interception: two trusted
// devices may swap addresses (DHCP, audit §3.5, #156).
func (t *TrustStore) KeyChangedAt(addr string, fp Fingerprint) (Peer, bool) {
	if addr == "" {
		return Peer{}, false
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	t.refreshLocked()
	if _, trusted := t.peers[fp]; trusted {
		return Peer{}, false
	}
	for _, p := range t.peers {
		if p.LastAddress == addr {
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
	return t.change(func(peers map[Fingerprint]Peer) bool {
		put(peers, p)
		return true
	})
}

// Update changes the trusted device fp as the file holds it now: edit reports whether it changed
// something, and a change another process made meanwhile is kept. Update reports whether fp is
// trusted.
func (t *TrustStore) Update(fp Fingerprint, edit func(*Peer) bool) (bool, error) {
	found := false
	err := t.change(func(peers map[Fingerprint]Peer) bool {
		p, ok := peers[fp]
		if found = ok; !ok || !edit(&p) {
			return false
		}
		p.Fingerprint = fp
		put(peers, p)
		return true
	})
	return found, err
}

// put sets p in peers. An address belongs to the device last seen there only: another device
// that had it loses it (§3.5).
func put(peers map[Fingerprint]Peer, p Peer) {
	if p.LastAddress != "" {
		for fp, q := range peers {
			if fp != p.Fingerprint && q.LastAddress == p.LastAddress {
				q.LastAddress = ""
				peers[fp] = q
			}
		}
	}
	peers[p.Fingerprint] = p
}

// Forget removes the device with fingerprint fp (ID-06). It reports whether it was trusted.
func (t *TrustStore) Forget(fp Fingerprint) (bool, error) {
	found := false
	err := t.change(func(peers map[Fingerprint]Peer) bool {
		_, found = peers[fp]
		delete(peers, fp)
		return found
	})
	if err != nil { // trust ends here even when the file cannot say so; the next write says it
		t.mu.Lock()
		_, found = t.peers[fp]
		delete(t.peers, fp)
		t.forgotten[fp] = true
		t.mu.Unlock()
		return found, err
	}
	return found, nil
}

// List returns the trusted devices, oldest first.
func (t *TrustStore) List() []Peer {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.refreshLocked()
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

func saveTrust(path string, peers map[Fingerprint]Peer) error {
	f := trustFile{Version: trustVersion, Peers: []peerJSON{}}
	for _, p := range peers {
		f.Peers = append(f.Peers, peerJSON{Fingerprint: p.Fingerprint.String(), Peer: p})
	}
	sort.Slice(f.Peers, func(i, j int) bool { return f.Peers[i].Fingerprint < f.Peers[j].Fingerprint })
	data, err := json.MarshalIndent(f, "", "  ")
	if err != nil {
		return err
	}
	return writeFileAtomic(path, append(data, '\n'), 0o600)
}
