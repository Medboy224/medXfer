package session

import (
	"crypto/tls"
	"encoding/json"
	"fmt"
	"net"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/Medboy224/medXfer/pkg/identity"
	"github.com/Medboy224/medXfer/pkg/manifest"
	"github.com/Medboy224/medXfer/pkg/protocol"
)

// DialPeer connects to a peer on the local network with an active retry and wake-up strategy.
// Mobile devices and Wi-Fi chipsets often sleep in power-save modes (802.11 DTIM)
// or have cold ARP tables that drop initial TCP SYN packets. DialPeer proactively
// triggers ARP/wake-up and retries, ensuring instant, reliable 1-click pairing.
func DialPeer(target string) (*net.TCPConn, error) {
	if !strings.Contains(target, ":") {
		target = fmt.Sprintf("%s:18887", target)
	}

	// Proactively probe with a UDP datagram to warm up ARP and wake sleeping Wi-Fi radios
	if udpConn, err := net.DialTimeout("udp4", target, 50*time.Millisecond); err == nil {
		_, _ = udpConn.Write([]byte{0})
		_ = udpConn.Close()
	}

	timeouts := []time.Duration{2500 * time.Millisecond, 3500 * time.Millisecond}
	var lastErr error

	for _, t := range timeouts {
		conn, err := net.DialTimeout("tcp4", target, t)
		if err == nil {
			if tcpConn, ok := conn.(*net.TCPConn); ok {
				_ = tcpConn.SetNoDelay(true)
				_ = tcpConn.SetKeepAlive(true)
				_ = tcpConn.SetKeepAlivePeriod(3 * time.Second)
				_ = tcpConn.SetReadBuffer(4 * 1024 * 1024)
				_ = tcpConn.SetWriteBuffer(4 * 1024 * 1024)
				return tcpConn, nil
			}
			return nil, fmt.Errorf("connection is not TCP")
		}
		lastErr = err
		time.Sleep(100 * time.Millisecond)
	}

	return nil, lastErr
}

type Message struct {
	Type        string               `json:"type"`
	DeviceName  string               `json:"device_name,omitempty"`
	PairingCode string               `json:"pairing_code,omitempty"`
	Secret      string               `json:"secret,omitempty"`      // QR pairing secret, base64url (pkg/pairing)
	Suite       string               `json:"suite,omitempty"`       // PIN pairing: the PAKE suite (pkg/pake), named in every pair_pin
	Share       []byte               `json:"share,omitempty"`       // PIN pairing: this side's PAKE share
	Confirm     []byte               `json:"confirm,omitempty"`     // PIN pairing: this side's key confirmation
	TrustsYou   bool                 `json:"trusts_you,omitempty"`  // pairing: the sender has the peer in its trusted list (PAIR-09)
	TrustOffer  bool                 `json:"trust_offer,omitempty"` // one-shot pairing: the sender wants mutual trust; the negotiation follows pair_accept (PAIR-10)
	TagKey      []byte               `json:"tag_key,omitempty"`     // trust_commit: the pair's recognition key (DISC-11)
	FileName    string               `json:"file_name,omitempty"`
	FileSize    int64                `json:"file_size,omitempty"`
	FileID      string               `json:"file_id,omitempty"` // Unique Cryptographic Hash
	DataPort    int                  `json:"data_port,omitempty"`
	ResumeBytes int64                `json:"resume_bytes,omitempty"`  // Sent back by the receiver on accept
	Batch       *manifest.Manifest   `json:"batch,omitempty"`         // Batch/Folder manifest
	ItemIndex   int                  `json:"item_index,omitempty"`    // Current item in batch (0-indexed)
	Item        *int                 `json:"item,omitempty"`          // skip, pause, resume: one item of the transfer; none: all of it
	TransferID  string               `json:"transfer_id,omitempty"`   // the transfer its data connections join (DATA_JOIN, CHAN-06)
	Offer       *protocol.Offer      `json:"offer,omitempty"`         // a transfer offer (bible §8.3); its head when paged
	Items       []protocol.OfferItem `json:"items,omitempty"`         // offer_items: a page of a paged offer (D2)
	ChunkSize   uint32               `json:"chunk_size,omitempty"`    // Chunk size used by sender (e.g. 4MB)
	BenchPingTS int64                `json:"bench_ping_ts,omitempty"` // Timestamp in nanoseconds for RTT calculation
	BenchSize   int64                `json:"bench_size,omitempty"`    // Requested size for bandwidth burst
	Error       string               `json:"error,omitempty"`         // Error message for item_failed or transfer failures
	Code        string               `json:"code,omitempty"`          // Machine-readable reason of an "error" message (bible §8.2)

	Progress *Progress `json:"progress,omitempty"` // progress: how far the receiver is
}

// TypeProgress carries the receiver's progress to the sender, a few times a second.
const TypeProgress = "progress"

// Progress is how far the receiver is: the bytes received and checked, of the whole transfer
// and of the items in progress. The sender shows it rather than what it served: both screens
// read the same, and a pause stops both bars (the chunks in flight count on neither).
type Progress struct {
	Bytes int64          `json:"bytes"`
	Items []ItemProgress `json:"items,omitempty"`
}

// ItemProgress is how far the receiver is in one item of the transfer.
type ItemProgress struct {
	Index int   `json:"index"`
	Bytes int64 `json:"bytes"`
}

// TypeFailed tells the peer that the transfer TransferID failed on this side, and why
// (Error): it ends its side too instead of waiting for it (WP-B.8). A peer that does not know
// it answers unsupported and keeps the old behaviour.
const TypeFailed = "failed"

// TypeError is the message that answers a message the peer cannot handle; its Code says why.
const TypeError = "error"

// CodeUnsupported answers a message type the peer does not know (bible §8.2).
const CodeUnsupported = "unsupported"

// Unsupported is the reply to a message whose type is unknown. Callers do not answer an
// "error" message with another one.
func Unsupported(msgType string) Message {
	return Message{Type: TypeError, Code: CodeUnsupported, Error: "unsupported message type " + msgType}
}

// Channel carries the JSON control messages of a session, one per CONTROL frame (bible §8.1):
// every read is bounded by the frame header (XFER-01, LIM-04), unlike a JSON stream decoder.
type Channel struct {
	conn       net.Conn
	sendMu     sync.Mutex
	closed     bool
	maxControl atomic.Uint32 // may change while another goroutine sends (audit §3.6)
	rbuf       []byte        // reused for small messages; read by one goroutine at a time
}

func NewChannel(conn net.Conn) *Channel {
	if tcpConn, ok := conn.(*net.TCPConn); ok {
		_ = tcpConn.SetNoDelay(true)
		_ = tcpConn.SetKeepAlive(true)
		_ = tcpConn.SetKeepAlivePeriod(2 * time.Second)
	}
	c := &Channel{conn: conn}
	c.maxControl.Store(protocol.MaxControl)
	return c
}

// SetMaxControl changes the largest control message accepted and sent (LIM-04 is a
// parameter, rule R4).
func (c *Channel) SetMaxControl(n uint32) { c.maxControl.Store(n) }

// sendTimeout bounds the write of one control message.
const sendTimeout = 5 * time.Second

// writeDeadlined is a connection that tells the write deadline its caller asked for
// (securechan.Conn).
type writeDeadlined interface {
	WriteDeadline() time.Time
}

func (c *Channel) Send(msg Message) error {
	payload, err := json.Marshal(msg)
	if err != nil {
		return err
	}
	if uint32(len(payload)) > c.maxControl.Load() {
		return fmt.Errorf("%w: %q message of %d bytes", protocol.ErrFrameTooLarge, msg.Type, len(payload))
	}
	frame := protocol.AppendFrame(make([]byte, 0, protocol.FrameHeaderSize+len(payload)), protocol.TypeControl, payload)
	c.sendMu.Lock()
	defer c.sendMu.Unlock()
	if c.closed || c.conn == nil {
		return net.ErrClosed
	}
	// The write is bounded, and the caller's own write deadline comes back after it: an
	// earlier one still bounds it (audit 7, #156).
	var prev time.Time
	if wd, ok := c.conn.(writeDeadlined); ok {
		prev = wd.WriteDeadline()
	}
	at := time.Now().Add(sendTimeout)
	if !prev.IsZero() && prev.Before(at) {
		at = prev
	}
	_ = c.conn.SetWriteDeadline(at)
	_, err = c.conn.Write(frame)
	_ = c.conn.SetWriteDeadline(prev)
	return err
}

// Read returns the next control message. A frame that is not CONTROL, or larger than the
// limit, or not valid JSON, is an error: the caller closes the channel.
func (c *Channel) Read() (Message, error) {
	_, payload, err := protocol.ReadFrame(c.conn, protocol.Limits{protocol.TypeControl: c.maxControl.Load()}, c.rbuf)
	if err != nil {
		return Message{}, err
	}
	if cap(payload) <= 64<<10 {
		c.rbuf = payload[:0] // keep small buffers only: a 1 MiB message is not held on to
	}
	var msg Message
	if err := json.Unmarshal(payload, &msg); err != nil {
		return Message{}, fmt.Errorf("session: invalid control message: %w", err)
	}
	return msg, nil
}

func (c *Channel) Close() {
	if c == nil {
		return
	}
	c.sendMu.Lock()
	c.closed = true
	c.sendMu.Unlock()
	if c.conn != nil {
		_ = c.conn.Close()
	}
}

func (c *Channel) IsClosed() bool {
	c.sendMu.Lock()
	defer c.sendMu.Unlock()
	return c.closed || c.conn == nil
}

func (c *Channel) RemoteIP() string {
	if c.conn != nil {
		host, _, _ := net.SplitHostPort(c.conn.RemoteAddr().String())
		return host
	}
	return "Unknown"
}

// IsEncrypted returns true if the underlying transport is secured with TLS.
func (c *Channel) IsEncrypted() bool {
	_, ok := c.conn.(interface{ ConnectionState() tls.ConnectionState })
	return ok
}

// PeerFingerprint is the verified key fingerprint of the peer when the channel runs over a
// secure channel (pkg/securechan); the zero value otherwise.
func (c *Channel) PeerFingerprint() identity.Fingerprint {
	if p, ok := c.conn.(interface{ PeerFingerprint() identity.Fingerprint }); ok {
		return p.PeerFingerprint()
	}
	return identity.Fingerprint{}
}

// Authenticate marks a pairing connection as authenticated (securechan state AUTH).
func (c *Channel) Authenticate() {
	if a, ok := c.conn.(interface{ Authenticate() }); ok {
		a.Authenticate()
	}
}
