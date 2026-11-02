package pairing

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/hkdf"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/binary"
	"encoding/hex"
	"errors"
	"io"
	"math/big"
	"net/netip"
	"slices"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/Medboy224/medXfer/pkg/identity"
	"github.com/Medboy224/medXfer/pkg/pake"
)

// The path by BLE (bible §6.6, J6d): two devices that share no network find each other by
// Bluetooth Low Energy, and one gives the other, encrypted, the network it created. The core
// makes and reads every message; the platform only carries them (advertising, scanning, GATT).
// Nothing stable goes in clear: no fingerprint, no name, no passphrase.
//
// The initiator (BLEClient) is the device whose user touches another in the list, a GATT
// client; the responder (BLEResponder, one BLEConn per GATT connection) is the device touched.

// BLEVersion is the version of the messages. It also fixes HKDF-SHA-256, HMAC-SHA-256 and
// AES-256-GCM: another of them is another version.
const BLEVersion = 1

// BLEMaxMessage bounds a message: the largest GATT attribute (BLE-03).
const BLEMaxMessage = 512

// BLEAuthLife is how long a key authenticated by BLE is accepted on the Wi-Fi, once (BLE-10).
const BLEAuthLife = 2 * time.Minute

// Parameters of bible §6.6 (rule R4).
const (
	bleRandLen      = 16 // c_I, c_R, n_I, n_R
	bleTagLen       = 8
	bleTagCount     = MaxTags // always this many in hello_reply (BLE-04)
	bleSuffixLen    = 2       // the generic name's 4 hex digits (DISC-07)
	bleKeyLen       = 32
	bleChallengeAge = 60 * time.Second // c_R is valid this long (BLE-04)
	bleMaxReceived  = 4096             // read on one connection at most (BLE-11, as LIM-03)

	bleTagInfo     = "medXfer-ble-v1 tag"
	bleTrustedInfo = "medXfer-ble-v1 trusted"
	blePINInfo     = "medXfer-ble-v1 pin"
	blePairService = "medXfer-ble-pair-v1" // the CI of CPace by BLE
)

// Message types (bible §6.6).
const (
	bleHello      byte = 1
	bleHelloReply byte = 2
	bleTrusted    byte = 3
	blePINWanted  byte = 4
	blePINReady   byte = 5
	blePIN        byte = 6
	blePINReply   byte = 7
	blePINConfirm byte = 8
	bleReply      byte = 9
	bleNetwork    byte = 10
	bleAck        byte = 11
	bleRefuse     byte = 15
)

var errBLEMessage = errors.New("pairing: malformed BLE message")

// bleEncode is a message: version ‖ type ‖ lv_cat(fields).
func bleEncode(typ byte, fields ...[]byte) []byte {
	return append([]byte{BLEVersion, typ}, pake.LVCat(fields...)...)
}

// bleDecode reads a message's type and fields. A message too long, of another version, or whose
// fields do not cut it exactly, is malformed.
func bleDecode(msg []byte) (byte, [][]byte, error) {
	if len(msg) < 2 || len(msg) > BLEMaxMessage || msg[0] != BLEVersion {
		return 0, nil, errBLEMessage
	}
	f, err := splitLV(msg[2:])
	return msg[1], f, err
}

// splitLV undoes lv_cat: LEB128 lengths in their shortest form, then the values.
func splitLV(b []byte) ([][]byte, error) {
	var out [][]byte
	for len(b) > 0 {
		n, used := 0, 0
		for shift := 0; ; shift += 7 {
			if used == len(b) || shift > 14 {
				return nil, errBLEMessage
			}
			c := b[used]
			used++
			n |= int(c&0x7f) << shift
			if c < 0x80 {
				if c == 0 && used > 1 {
					return nil, errBLEMessage // not the shortest form
				}
				break
			}
		}
		if n > len(b)-used {
			return nil, errBLEMessage
		}
		out = append(out, b[used:used+n])
		b = b[used+n:]
	}
	return out, nil
}

// sized reports whether fields has one field per size, each of that size; -1 takes any.
func sized(fields [][]byte, sizes ...int) bool {
	if len(fields) != len(sizes) {
		return false
	}
	for i, s := range sizes {
		if s >= 0 && len(fields[i]) != s {
			return false
		}
	}
	return true
}

func bleTagKey(pairKey []byte) ([]byte, error) {
	return hkdf.Key(sha256.New, pairKey, nil, bleTagInfo, bleKeyLen)
}

// bleTag tells a device of mutual trust "it is me" for challenge c: it only designates the
// pair's key; decrypting is what authenticates.
func bleTag(tagKey []byte, fp identity.Fingerprint, c []byte) []byte {
	m := hmac.New(sha256.New, tagKey)
	m.Write(fp[:])
	m.Write(c)
	return m.Sum(nil)[:bleTagLen]
}

// bleBox encrypts one direction of an exchange: AES-256-GCM, the nonce the number of messages
// it sealed (never sent), the AD the message's version and type. Its key is new to each
// exchange, so a message replayed, lost or moved does not open.
type bleBox struct {
	aead cipher.AEAD
	n    uint64
}

func newBLEBox(key []byte) (*bleBox, error) {
	b, err := aes.NewCipher(key)
	if err != nil {
		return nil, err
	}
	g, err := cipher.NewGCM(b)
	if err != nil {
		return nil, err
	}
	return &bleBox{aead: g}, nil
}

func (b *bleBox) nonce() []byte {
	var n [12]byte
	binary.BigEndian.PutUint64(n[4:], b.n)
	return n[:]
}

func (b *bleBox) seal(typ byte, fields ...[]byte) []byte {
	ct := b.aead.Seal(nil, b.nonce(), pake.LVCat(fields...), []byte{BLEVersion, typ})
	b.n++
	return ct
}

func (b *bleBox) open(typ byte, ct []byte) ([][]byte, error) {
	pt, err := b.aead.Open(nil, b.nonce(), ct, []byte{BLEVersion, typ})
	if err != nil {
		return nil, errBLEMessage
	}
	b.n++
	return splitLV(pt)
}

// bleBoxes are the two directions of an exchange, from 64 bytes of key: K_i2r ‖ K_r2i.
func bleBoxes(key []byte) (i2r, r2i *bleBox, err error) {
	if i2r, err = newBLEBox(key[:bleKeyLen]); err != nil {
		return nil, nil, err
	}
	r2i, err = newBLEBox(key[bleKeyLen:])
	return i2r, r2i, err
}

// bleTrustedBoxes: HKDF-SHA-256(K_AB, c_I ‖ c_R, info ‖ fp_I ‖ fp_R), the path of trust.
func bleTrustedBoxes(pairKey, cI, cR []byte, fpI, fpR identity.Fingerprint) (*bleBox, *bleBox, error) {
	info := bleTrustedInfo + string(fpI[:]) + string(fpR[:])
	key, err := hkdf.Key(sha256.New, pairKey, slices.Concat(cI, cR), info, 2*bleKeyLen)
	if err != nil {
		return nil, nil, err
	}
	return bleBoxes(key)
}

// blePINBoxes: HKDF-SHA-256(ISK, "medXfer-ble-v1 pin"), the path of the PIN.
func blePINBoxes(s *pake.Session) (*bleBox, *bleBox, error) {
	key, err := s.Key(blePINInfo, 2*bleKeyLen)
	if err != nil {
		return nil, nil, err
	}
	return bleBoxes(key)
}

// blePINParams are CPace's inputs by BLE: no TLS, so no ekm; the sid is made of the nonces of
// both sides (draft §10.9), and the CI holds no fingerprint, which would go in clear. The
// fingerprints travel encrypted after the confirmation (bible §6.6).
func blePINParams(code, nI, nR []byte, role pake.Role) pake.Params {
	return pake.Params{Password: code, CI: pake.LVCat([]byte(blePairService)), SID: slices.Concat(nI, nR), Role: role}
}

func bleRandom(r io.Reader, n int) ([]byte, error) {
	b := make([]byte, n)
	_, err := io.ReadFull(r, b)
	return b, err
}

func encodeSuffix(s string) ([]byte, error) {
	b, err := hex.DecodeString(s)
	if err != nil || len(b) != bleSuffixLen {
		return nil, errors.New("pairing: a generic name's suffix is 4 hex digits")
	}
	return b, nil
}

// BLEBands are Wi-Fi bands, as bits.
type BLEBands byte

const (
	BLEBand24 BLEBands = 1 << 0
	BLEBand5  BLEBands = 1 << 1

	bleBandsKnown = BLEBand24 | BLEBand5
)

// BLERequest is what a device can do with a network: the bands it can create one on, and join
// one on.
type BLERequest struct {
	Create, Join BLEBands
}

func (r BLERequest) fields() [][]byte { return [][]byte{{byte(r.Create)}, {byte(r.Join)}} }

func readRequest(f [][]byte) (BLERequest, bool) {
	if !sized(f, 1, 1) || BLEBands(f[0][0])&^bleBandsKnown != 0 || BLEBands(f[1][0])&^bleBandsKnown != 0 {
		return BLERequest{}, false
	}
	return BLERequest{Create: BLEBands(f[0][0]), Join: BLEBands(f[1][0])}, true
}

// BLEOutcome says who creates the network.
type BLEOutcome byte

const (
	BLEResponderCreates BLEOutcome = 1
	BLEInitiatorCreates BLEOutcome = 2
	BLENoNetwork        BLEOutcome = 3
)

// Creator is the rule of bible §6.6: the one that can create the network at 5 GHz when the
// other can join it there, else at 2.4 GHz; the responder when both can; none otherwise.
func Creator(initiator, responder BLERequest) BLEOutcome {
	for _, band := range []BLEBands{BLEBand5, BLEBand24} {
		if responder.Create&band != 0 && initiator.Join&band != 0 {
			return BLEResponderCreates
		}
		if initiator.Create&band != 0 && responder.Join&band != 0 {
			return BLEInitiatorCreates
		}
	}
	return BLENoNetwork
}

// BLENetwork is the network a device created for the occasion (DISC-12), as BLE carries it,
// encrypted. Never a network of the user's own (BLE-09).
type BLENetwork struct {
	SSID       string
	Passphrase string // a secret of the network: never logged
	Security   byte   // 1 WPA2, 2 WPA3 transition, 3 WPA3: the QR code's wt
	Band       BLEBands
	Port       uint16 // the creator's medXfer port
	Addrs      []netip.Addr
}

func (n BLENetwork) valid() bool {
	if len(n.SSID) < 1 || len(n.SSID) > 32 || len(n.Passphrase) < 8 || len(n.Passphrase) > 63 {
		return false
	}
	for _, c := range []byte(n.Passphrase) {
		if c < 0x20 || c > 0x7e {
			return false
		}
	}
	if n.Security < 1 || n.Security > 3 || (n.Band != BLEBand24 && n.Band != BLEBand5) || n.Port == 0 || len(n.Addrs) < 1 || len(n.Addrs) > 4 {
		return false
	}
	for _, a := range n.Addrs {
		if !a.IsValid() || a.IsUnspecified() || a.Zone() != "" {
			return false
		}
	}
	return true
}

func (n BLENetwork) fields() ([][]byte, error) {
	if !n.valid() {
		return nil, errors.New("pairing: invalid network")
	}
	port := binary.BigEndian.AppendUint16(nil, n.Port)
	f := [][]byte{[]byte(n.SSID), []byte(n.Passphrase), {n.Security}, {byte(n.Band)}, port}
	for _, a := range n.Addrs {
		f = append(f, a.Unmap().AsSlice())
	}
	return f, nil
}

func readNetwork(f [][]byte) (BLENetwork, bool) {
	if len(f) < 6 || !sized(f[:5], -1, -1, 1, 1, 2) {
		return BLENetwork{}, false
	}
	n := BLENetwork{SSID: string(f[0]), Passphrase: string(f[1]), Security: f[2][0], Band: BLEBands(f[3][0]), Port: binary.BigEndian.Uint16(f[4])}
	for _, b := range f[5:] {
		a, ok := netip.AddrFromSlice(b)
		if !ok {
			return BLENetwork{}, false
		}
		n.Addrs = append(n.Addrs, a)
	}
	return n, n.valid()
}

// BLEAuthenticated holds the keys BLE authenticated, for the TLS connection that follows on the
// network (BLE-10): each is accepted once, within BLEAuthLife. It is safe for concurrent use.
type BLEAuthenticated struct {
	Now func() time.Time // time.Now when nil

	mu sync.Mutex
	m  map[identity.Fingerprint]time.Time
}

func (a *BLEAuthenticated) now() time.Time {
	if a.Now != nil {
		return a.Now()
	}
	return time.Now()
}

// Add accepts fp for one connection, from now for BLEAuthLife.
func (a *BLEAuthenticated) Add(fp identity.Fingerprint) {
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.m == nil {
		a.m = map[identity.Fingerprint]time.Time{}
	}
	a.m[fp] = a.now().Add(BLEAuthLife)
}

// Take reports whether fp was authenticated by BLE and not taken yet, and takes it.
func (a *BLEAuthenticated) Take(fp identity.Fingerprint) bool {
	a.mu.Lock()
	defer a.mu.Unlock()
	until, ok := a.m[fp]
	delete(a.m, fp)
	return ok && a.now().Before(until)
}

// BLEResponderConfig sets up the device touched.
type BLEResponderConfig struct {
	Self   identity.Fingerprint
	Suffix string // the generic name's 4 hex digits (DISC-07)
	Trust  *identity.TrustStore
	Own    BLERequest // what this device can create and join
	// PIN gets the PIN on demand (PAIR-11) for the unknown device whose generic name ends with
	// suffix, and shows the request with it; nil when no PIN may be given by BLE. It is the PIN
	// of the network too: one PIN, one count of attempts (BLE-07).
	PIN  func(suffix string) (*PIN, error)
	Rand io.Reader        // crypto/rand when nil
	Now  func() time.Time // time.Now when nil
}

// BLEResponder answers the devices that connect to this one's GATT service. It is safe for
// concurrent use.
type BLEResponder struct {
	cfg BLEResponderConfig

	mu       sync.Mutex
	pinConn  *BLEConn  // the one request of a PIN on screen (BLE-07)
	pinSince time.Time // when it came: past a PIN's life, another may take its place
}

// NewBLEResponder returns a responder for c.
func NewBLEResponder(c BLEResponderConfig) (*BLEResponder, error) {
	if _, err := encodeSuffix(c.Suffix); err != nil {
		return nil, err
	}
	if c.Rand == nil {
		c.Rand = rand.Reader
	}
	if c.Now == nil {
		c.Now = time.Now
	}
	return &BLEResponder{cfg: c}, nil
}

// Conn is the state of one GATT client's connection. The platform gives it each message that
// client wrote, and sends back what Receive or Answer gives; Close when the connection goes.
func (r *BLEResponder) Conn() *BLEConn { return &BLEConn{r: r} }

// RefusePIN refuses the request of a PIN on screen (the user's "Refuser"): its next message is
// refused.
func (r *BLEResponder) RefusePIN() {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.pinConn != nil {
		r.pinConn.refused.Store(true)
	}
}

// BLEStep is what a message received by the responder gives: a reply to send, or a request
// authenticated, which waits for the network (Answer), or the network the initiator created.
type BLEStep struct {
	Reply   []byte
	Request *BLEAuthRequest
	Network *BLENetwork // Reply acknowledges it: join it, then connect (BLE-10)
}

// BLEAuthRequest is a request whose sender BLE authenticated: by the pair's key (Trusted) or
// by the PIN. The platform gives the network by Answer.
type BLEAuthRequest struct {
	Peer    identity.Fingerprint
	Trusted bool
	Request BLERequest
	Outcome BLEOutcome // Creator(Request, own)
}

const (
	bleIdle = iota
	bleHelloed
	blePINShown
	blePINAnswered
	bleAuthenticated
	bleAwaitNetwork
	bleDone
	bleClosed
)

// BLEConn is one GATT client's connection to the responder.
type BLEConn struct {
	r        *BLEResponder
	mu       sync.Mutex
	stage    int
	received int

	cI, cR  []byte
	helloAt time.Time

	nI, nR  []byte
	pin     *PIN
	shownAt time.Time
	refused atomic.Bool // set by RefusePIN, under the responder's lock
	session *pake.Session
	attempt func(paired bool) // ends the attempt on the PIN, once; nil when none runs

	peer     identity.Fingerprint
	trusted  bool
	i2r, r2i *bleBox
}

// Receive takes a message the client wrote. On ErrRefused the step's Reply is refuse: send it,
// then close the connection.
func (c *BLEConn) Receive(msg []byte) (BLEStep, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.received += len(msg)
	typ, f, err := bleDecode(msg)
	if err != nil || c.received > bleMaxReceived {
		return c.refuse()
	}
	switch {
	case typ == bleHello && (c.stage == bleIdle || c.stage == bleHelloed):
		return c.hello(f)
	case typ == bleTrusted && c.stage == bleHelloed:
		return c.trustedRequest(f)
	case typ == blePINWanted && (c.stage == bleIdle || c.stage == bleHelloed):
		return c.pinWanted(f)
	case typ == blePIN && c.stage == blePINShown:
		return c.pinTyped(f)
	case typ == blePINConfirm && c.stage == blePINAnswered:
		return c.pinConfirm(f)
	case typ == bleNetwork && c.stage == bleAwaitNetwork:
		return c.network(f)
	}
	return c.refuse()
}

// refuse ends the connection: a refusal that says nothing (PAIR-07). An attempt on the PIN
// begun and not confirmed counts (PAIR-04).
func (c *BLEConn) refuse() (BLEStep, error) {
	c.closeLocked()
	return BLEStep{Reply: bleEncode(bleRefuse)}, ErrRefused
}

// Close ends the connection, as the platform saw it go: an attempt begun counts.
func (c *BLEConn) Close() {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.closeLocked()
}

func (c *BLEConn) closeLocked() {
	c.stage = bleClosed
	c.endAttempt(false)
	c.r.mu.Lock()
	if c.r.pinConn == c {
		c.r.pinConn = nil
	}
	c.r.mu.Unlock()
}

func (c *BLEConn) endAttempt(paired bool) {
	if c.attempt != nil {
		c.attempt(paired)
		c.attempt = nil
	}
}

// hello: the 16 tags of this device for the client's challenge (BLE-04), its suffix, and its
// own challenge.
func (c *BLEConn) hello(f [][]byte) (BLEStep, error) {
	cfg := &c.r.cfg
	if !sized(f, bleRandLen) {
		return c.refuse()
	}
	cR, err := bleRandom(cfg.Rand, bleRandLen)
	if err != nil {
		return c.refuse()
	}
	tags := make([][]byte, 0, bleTagCount)
	for _, p := range tagged(cfg.Trust) {
		if len(tags) == bleTagCount {
			break
		}
		key, err := bleTagKey(p.TagKey)
		if err != nil {
			return c.refuse()
		}
		tags = append(tags, bleTag(key, cfg.Self, f[0]))
	}
	for len(tags) < bleTagCount { // the free places random: their number says nothing
		t, err := bleRandom(cfg.Rand, bleTagLen)
		if err != nil {
			return c.refuse()
		}
		tags = append(tags, t)
	}
	for i := len(tags) - 1; i > 0; i-- { // in a random order
		j, err := rand.Int(cfg.Rand, big.NewInt(int64(i+1)))
		if err != nil {
			return c.refuse()
		}
		tags[i], tags[j.Int64()] = tags[j.Int64()], tags[i]
	}
	suffix, _ := encodeSuffix(cfg.Suffix)
	c.cI, c.cR, c.helloAt, c.stage = slices.Clone(f[0]), cR, cfg.Now(), bleHelloed
	return BLEStep{Reply: bleEncode(bleHelloReply, slices.Concat(tags...), suffix, cR)}, nil
}

// trustedRequest: a device of mutual trust asks for the network (BLE-05). Its tag designates
// the pair; the request opens only under that pair's key and this connection's challenges.
func (c *BLEConn) trustedRequest(f [][]byte) (BLEStep, error) {
	cfg := &c.r.cfg
	if !sized(f, bleTagLen, -1) || cfg.Now().Sub(c.helloAt) > bleChallengeAge {
		return c.refuse()
	}
	for _, p := range tagged(cfg.Trust) {
		key, err := bleTagKey(p.TagKey)
		if err != nil || !hmac.Equal(bleTag(key, p.Fingerprint, c.cR), f[0]) {
			continue
		}
		i2r, r2i, err := bleTrustedBoxes(p.TagKey, c.cI, c.cR, p.Fingerprint, cfg.Self)
		if err != nil {
			continue
		}
		body, err := i2r.open(bleTrusted, f[1])
		if err != nil {
			continue
		}
		req, ok := readRequest(body)
		if !ok {
			break
		}
		c.peer, c.trusted, c.i2r, c.r2i, c.stage = p.Fingerprint, true, i2r, r2i, bleAuthenticated
		return BLEStep{Request: &BLEAuthRequest{Peer: p.Fingerprint, Trusted: true, Request: req, Outcome: Creator(req, cfg.Own)}}, nil
	}
	return c.refuse()
}

// pinWanted: an unknown device asks for a PIN (BLE-07). The request shows on screen with its
// suffix, the PIN and "Refuser"; one at a time.
func (c *BLEConn) pinWanted(f [][]byte) (BLEStep, error) {
	cfg := &c.r.cfg
	if !sized(f, bleRandLen, bleSuffixLen) || cfg.PIN == nil {
		return c.refuse()
	}
	// A connection gone without the platform seeing it would hold the screen: past a PIN's life
	// its request is refused, and the new one takes its place.
	now := cfg.Now()
	c.r.mu.Lock()
	old := c.r.pinConn
	busy := old != nil && old != c && now.Sub(c.r.pinSince) <= typingTime
	if !busy {
		if old != nil && old != c {
			old.refused.Store(true)
		}
		c.r.pinConn, c.r.pinSince = c, now
	}
	c.r.mu.Unlock()
	if busy {
		return c.refuse()
	}
	p, err := cfg.PIN(strings.ToUpper(hex.EncodeToString(f[1])))
	if err != nil || p == nil || !p.Live() {
		return c.refuse()
	}
	nR, err := bleRandom(cfg.Rand, bleRandLen)
	if err != nil {
		return c.refuse()
	}
	c.nI, c.nR, c.pin, c.shownAt, c.stage = slices.Clone(f[0]), nR, p, cfg.Now(), blePINShown
	return BLEStep{Reply: bleEncode(blePINReady, nR)}, nil
}

// pinTyped: CPace on the PIN typed (§6.2, BLE-06). The attempt counts from the reply on
// (PAIR-04); with none left, the exchange runs on a decoy, which the client cannot tell
// (PAIR-07).
func (c *BLEConn) pinTyped(f [][]byte) (BLEStep, error) {
	cfg := &c.r.cfg
	if !sized(f, -1, -1) || c.refused.Load() || cfg.Now().Sub(c.shownAt) > typingTime || !slices.Contains(pake.Suites(), string(f[0])) {
		return c.refuse()
	}
	code, counted := c.pin.begin()
	if counted {
		p := c.pin
		c.attempt = p.end
	} else {
		var err error
		if code, err = drawPIN(c.pin.cfg.Rand, c.pin.cfg.Digits); err != nil {
			return c.refuse()
		}
	}
	ex, err := pake.New(string(f[0]), blePINParams(code, c.nI, c.nR, pake.Responder))
	if err != nil {
		return c.refuse()
	}
	s, err := ex.Finish(f[1])
	if err != nil {
		return c.refuse()
	}
	c.session, c.stage = s, blePINAnswered
	return BLEStep{Reply: bleEncode(blePINReply, ex.Share(), s.Tag())}, nil
}

// pinConfirm: the client confirmed the key, and sends its fingerprint and request under it.
func (c *BLEConn) pinConfirm(f [][]byte) (BLEStep, error) {
	if !sized(f, -1, -1) || c.refused.Load() || c.attempt == nil || !c.session.Verify(f[0]) {
		return c.refuse()
	}
	i2r, r2i, err := blePINBoxes(c.session)
	if err != nil {
		return c.refuse()
	}
	body, err := i2r.open(blePINConfirm, f[1])
	if err != nil || len(body) != 3 || len(body[0]) != len(identity.Fingerprint{}) {
		return c.refuse()
	}
	req, ok := readRequest(body[1:])
	if !ok {
		return c.refuse()
	}
	c.endAttempt(true) // the PIN is used (PAIR-02)
	c.r.mu.Lock()
	if c.r.pinConn == c {
		c.r.pinConn = nil
	}
	c.r.mu.Unlock()
	c.peer = identity.Fingerprint(body[0])
	c.i2r, c.r2i, c.stage = i2r, r2i, bleAuthenticated
	return BLEStep{Request: &BLEAuthRequest{Peer: c.peer, Request: req, Outcome: Creator(req, c.r.cfg.Own)}}, nil
}

// Answer replies to the request authenticated: outcome, and the network when this device
// created it (BLEResponderCreates). On the path of the PIN, this device's fingerprint goes
// with it, encrypted.
func (c *BLEConn) Answer(outcome BLEOutcome, n *BLENetwork) ([]byte, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.stage != bleAuthenticated || outcome < BLEResponderCreates || outcome > BLENoNetwork || (outcome == BLEResponderCreates) != (n != nil) {
		return nil, errors.New("pairing: no request to answer this way")
	}
	var body [][]byte
	if !c.trusted {
		body = append(body, c.r.cfg.Self[:])
	}
	body = append(body, []byte{byte(outcome)})
	if n != nil {
		nf, err := n.fields()
		if err != nil {
			return nil, err
		}
		body = append(body, nf...)
	}
	c.stage = bleDone
	if outcome == BLEInitiatorCreates {
		c.stage = bleAwaitNetwork
	}
	return bleEncode(bleReply, c.r2i.seal(bleReply, body...)), nil
}

// network: the initiator created the network; acknowledged.
func (c *BLEConn) network(f [][]byte) (BLEStep, error) {
	if !sized(f, -1) {
		return c.refuse()
	}
	body, err := c.i2r.open(bleNetwork, f[0])
	if err != nil {
		return c.refuse()
	}
	n, ok := readNetwork(body)
	if !ok {
		return c.refuse()
	}
	c.stage = bleDone
	return BLEStep{Network: &n, Reply: bleEncode(bleAck, c.r2i.seal(bleAck))}, nil
}

// BLEClientConfig sets up the device that touches another.
type BLEClientConfig struct {
	Self   identity.Fingerprint
	Suffix string // the generic name's 4 hex digits (DISC-07)
	Trust  *identity.TrustStore
	Rand   io.Reader // crypto/rand when nil
}

// BLEClient is one exchange of the initiator with one responder, over one GATT connection.
// Each method makes the next message to write, or reads the responder's answer; a refusal, or
// anything amiss, is ErrRefused. It is not for concurrent use.
type BLEClient struct {
	cfg BLEClientConfig

	cI, cR  []byte
	partner *identity.Peer

	nI, nR  []byte
	ex      pake.Exchange
	session *pake.Session

	pinPath  bool
	peer     identity.Fingerprint
	i2r, r2i *bleBox
}

// NewBLEClient returns an exchange for c.
func NewBLEClient(c BLEClientConfig) (*BLEClient, error) {
	if _, err := encodeSuffix(c.Suffix); err != nil {
		return nil, err
	}
	if c.Rand == nil {
		c.Rand = rand.Reader
	}
	return &BLEClient{cfg: c}, nil
}

// BLEPeer is what the recognition tells of a responder: its generic name's suffix, and the
// device of mutual trust it is, if any.
type BLEPeer struct {
	Suffix  string
	Trusted *identity.Peer
}

// read decodes the responder's answer of type want: refuse, or anything else, is ErrRefused.
func (c *BLEClient) read(msg []byte, want byte) ([][]byte, error) {
	typ, f, err := bleDecode(msg)
	if err != nil || typ != want {
		return nil, ErrRefused
	}
	return f, nil
}

// Hello is the recognition's message, with a fresh challenge (BLE-04).
func (c *BLEClient) Hello() ([]byte, error) {
	cI, err := bleRandom(c.cfg.Rand, bleRandLen)
	if err != nil {
		return nil, err
	}
	c.cI = cI
	return bleEncode(bleHello, cI), nil
}

// Recognize reads hello_reply: the responder's suffix, and which device of mutual trust it is
// when one of its tags is that device's for this challenge.
func (c *BLEClient) Recognize(msg []byte) (BLEPeer, error) {
	f, err := c.read(msg, bleHelloReply)
	if err != nil || c.cI == nil || !sized(f, bleTagCount*bleTagLen, bleSuffixLen, bleRandLen) {
		return BLEPeer{}, ErrRefused
	}
	c.cR = slices.Clone(f[2])
	out := BLEPeer{Suffix: strings.ToUpper(hex.EncodeToString(f[1]))}
	for _, p := range tagged(c.cfg.Trust) {
		key, err := bleTagKey(p.TagKey)
		if err != nil {
			continue
		}
		want := bleTag(key, p.Fingerprint, c.cI)
		found := 0
		for i := 0; i < bleTagCount; i++ {
			found |= subtle.ConstantTimeCompare(want, f[0][i*bleTagLen:(i+1)*bleTagLen])
		}
		if found == 1 {
			out.Trusted = &p
			c.partner = &p
			return out, nil
		}
	}
	return out, nil
}

// Trusted asks the device of mutual trust recognized for the network (BLE-05).
func (c *BLEClient) Trusted(req BLERequest) ([]byte, error) {
	if c.partner == nil || c.cR == nil {
		return nil, errors.New("pairing: no device of mutual trust recognized")
	}
	key, err := bleTagKey(c.partner.TagKey)
	if err != nil {
		return nil, err
	}
	i2r, r2i, err := bleTrustedBoxes(c.partner.TagKey, c.cI, c.cR, c.cfg.Self, c.partner.Fingerprint)
	if err != nil {
		return nil, err
	}
	c.i2r, c.r2i, c.peer, c.pinPath = i2r, r2i, c.partner.Fingerprint, false
	return bleEncode(bleTrusted, bleTag(key, c.cfg.Self, c.cR), i2r.seal(bleTrusted, req.fields()...)), nil
}

// PINWanted asks an unknown device for a PIN, which it shows with this device's suffix.
func (c *BLEClient) PINWanted() ([]byte, error) {
	nI, err := bleRandom(c.cfg.Rand, bleRandLen)
	if err != nil {
		return nil, err
	}
	suffix, err := encodeSuffix(c.cfg.Suffix)
	if err != nil {
		return nil, err
	}
	c.nI = nI
	return bleEncode(blePINWanted, nI, suffix), nil
}

// PINReady reads pin_ready: the PIN is on the responder's screen.
func (c *BLEClient) PINReady(msg []byte) error {
	f, err := c.read(msg, blePINReady)
	if err != nil || c.nI == nil || !sized(f, bleRandLen) {
		return ErrRefused
	}
	c.nR = slices.Clone(f[0])
	return nil
}

// PIN is CPace's first message, on the PIN the user typed.
func (c *BLEClient) PIN(pin string) ([]byte, error) {
	code, err := NormalizePIN(pin)
	if err != nil {
		return nil, err // nothing is sent for a PIN mistyped
	}
	if c.nR == nil {
		return nil, errors.New("pairing: no PIN asked for")
	}
	ex, err := pake.New(PINSuite, blePINParams(code, c.nI, c.nR, pake.Initiator))
	if err != nil {
		return nil, err
	}
	c.ex = ex
	return bleEncode(blePIN, []byte(PINSuite), ex.Share()), nil
}

// PINReply reads the responder's confirmation, and only if it passes confirms in turn, with
// this device's fingerprint and request encrypted (PAIR-05, BLE-06). A wrong PIN is ErrRefused.
func (c *BLEClient) PINReply(msg []byte, req BLERequest) ([]byte, error) {
	f, err := c.read(msg, blePINReply)
	if err != nil || c.ex == nil || !sized(f, -1, -1) {
		return nil, ErrRefused
	}
	s, err := c.ex.Finish(f[0])
	if err != nil || !s.Verify(f[1]) {
		return nil, ErrRefused
	}
	i2r, r2i, err := blePINBoxes(s)
	if err != nil {
		return nil, err
	}
	c.session, c.i2r, c.r2i, c.pinPath = s, i2r, r2i, true
	return bleEncode(blePINConfirm, s.Tag(), i2r.seal(blePINConfirm, append([][]byte{c.cfg.Self[:]}, req.fields()...)...)), nil
}

// BLEAnswer is the responder's reply: who it is (authenticated), who creates the network, and
// the network when the responder created it.
type BLEAnswer struct {
	Peer    identity.Fingerprint
	Outcome BLEOutcome
	Network *BLENetwork
}

// Reply reads the responder's reply to the request.
func (c *BLEClient) Reply(msg []byte) (BLEAnswer, error) {
	f, err := c.read(msg, bleReply)
	if err != nil || c.r2i == nil || !sized(f, -1) {
		return BLEAnswer{}, ErrRefused
	}
	body, err := c.r2i.open(bleReply, f[0])
	if err != nil {
		return BLEAnswer{}, ErrRefused
	}
	if c.pinPath {
		if len(body) == 0 || len(body[0]) != len(identity.Fingerprint{}) {
			return BLEAnswer{}, ErrRefused
		}
		c.peer, body = identity.Fingerprint(body[0]), body[1:]
	}
	if len(body) == 0 || len(body[0]) != 1 {
		return BLEAnswer{}, ErrRefused
	}
	a := BLEAnswer{Peer: c.peer, Outcome: BLEOutcome(body[0][0])}
	switch {
	case a.Outcome == BLEResponderCreates:
		n, ok := readNetwork(body[1:])
		if !ok {
			return BLEAnswer{}, ErrRefused
		}
		a.Network = &n
	case (a.Outcome == BLEInitiatorCreates || a.Outcome == BLENoNetwork) && len(body) == 1:
	default:
		return BLEAnswer{}, ErrRefused
	}
	return a, nil
}

// Network gives the responder the network this device created (BLEInitiatorCreates).
func (c *BLEClient) Network(n BLENetwork) ([]byte, error) {
	if c.i2r == nil {
		return nil, errors.New("pairing: no exchange")
	}
	nf, err := n.fields()
	if err != nil {
		return nil, err
	}
	return bleEncode(bleNetwork, c.i2r.seal(bleNetwork, nf...)), nil
}

// Ack reads the responder's acknowledgement of the network.
func (c *BLEClient) Ack(msg []byte) error {
	f, err := c.read(msg, bleAck)
	if err != nil || !sized(f, -1) {
		return ErrRefused
	}
	body, err := c.r2i.open(bleAck, f[0])
	if err != nil || len(body) != 0 {
		return ErrRefused
	}
	return nil
}
