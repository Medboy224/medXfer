package session

import (
	"encoding/json"
	"net"

	"github.com/Medboy224/medXfer/pkg/manifest"
)

type Message struct {
	Type        string             `json:"type"`
	FileName    string             `json:"file_name,omitempty"`
	FileSize    int64              `json:"file_size,omitempty"`
	FileID      string             `json:"file_id,omitempty"` // Unique Cryptographic Hash
	DataPort    int                `json:"data_port,omitempty"`
	ResumeBytes int64              `json:"resume_bytes,omitempty"` // Sent back by the receiver on accept
	Batch       *manifest.Manifest `json:"batch,omitempty"`        // Batch/Folder manifest
	ItemIndex   int                `json:"item_index,omitempty"`   // Current item in batch (0-indexed)
}

type Channel struct {
	conn *net.TCPConn
	dec  *json.Decoder
	enc  *json.Encoder
}

func NewChannel(conn *net.TCPConn) *Channel {
	if conn != nil {
		_ = conn.SetNoDelay(true)
		_ = conn.SetKeepAlive(true)
	}
	return &Channel{
		conn: conn,
		dec:  json.NewDecoder(conn),
		enc:  json.NewEncoder(conn),
	}
}

func (c *Channel) Send(msg Message) error { return c.enc.Encode(msg) }
func (c *Channel) Read() (Message, error) {
	var msg Message
	err := c.dec.Decode(&msg)
	return msg, err
}
func (c *Channel) Close() {
	if c.conn != nil {
		c.conn.Close()
	}
}
func (c *Channel) RemoteIP() string {
	if c.conn != nil {
		host, _, _ := net.SplitHostPort(c.conn.RemoteAddr().String())
		return host
	}
	return "Unknown"
}
