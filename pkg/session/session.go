package session

import (
	"crypto/tls"
	"encoding/json"
	"fmt"
	"net"
	"strings"
	"sync"
	"time"

	"github.com/Medboy224/medXfer/pkg/manifest"
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
	Type        string             `json:"type"`
	DeviceName  string             `json:"device_name,omitempty"`
	FileName    string             `json:"file_name,omitempty"`
	FileSize    int64              `json:"file_size,omitempty"`
	FileID      string             `json:"file_id,omitempty"` // Unique Cryptographic Hash
	DataPort    int                `json:"data_port,omitempty"`
	ResumeBytes int64              `json:"resume_bytes,omitempty"` // Sent back by the receiver on accept
	Batch       *manifest.Manifest `json:"batch,omitempty"`        // Batch/Folder manifest
	ItemIndex   int                `json:"item_index,omitempty"`   // Current item in batch (0-indexed)
	IsStream    bool               `json:"is_stream,omitempty"`    // On-The-Fly container stream for folders/small files
}

type Channel struct {
	conn   net.Conn
	dec    *json.Decoder
	enc    *json.Encoder
	sendMu sync.Mutex
	closed bool
}

func NewChannel(conn net.Conn) *Channel {
	if tcpConn, ok := conn.(*net.TCPConn); ok {
		_ = tcpConn.SetNoDelay(true)
		_ = tcpConn.SetKeepAlive(true)
		_ = tcpConn.SetKeepAlivePeriod(2 * time.Second)
	}
	return &Channel{
		conn: conn,
		dec:  json.NewDecoder(conn),
		enc:  json.NewEncoder(conn),
	}
}

func (c *Channel) Send(msg Message) error {
	c.sendMu.Lock()
	defer c.sendMu.Unlock()
	if c.closed || c.conn == nil {
		return net.ErrClosed
	}
	_ = c.conn.SetWriteDeadline(time.Now().Add(5 * time.Second))
	err := c.enc.Encode(msg)
	_ = c.conn.SetWriteDeadline(time.Time{})
	return err
}

func (c *Channel) Read() (Message, error) {
	var msg Message
	err := c.dec.Decode(&msg)
	return msg, err
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

// IsEncrypted returns true if the underlying transport is secured with TLS
func (c *Channel) IsEncrypted() bool {
	if c.conn != nil {
		_, ok := c.conn.(*tls.Conn)
		return ok
	}
	return false
}
