package session

import (
	"encoding/json"
	"net"
)

// Message is the JSON payload sent over the persistent control channel
type Message struct {
	Type     string `json:"type"` // "offer", "accept", "reject", "disconnect"
	FileName string `json:"file_name,omitempty"`
	FileSize int64  `json:"file_size,omitempty"`
	DataPort int    `json:"data_port,omitempty"` // The port where the high-speed engine is waiting
}

// Channel wraps the persistent TCP connection
type Channel struct {
	conn *net.TCPConn
	dec  *json.Decoder
	enc  *json.Encoder
}

// NewChannel creates a new session manager from an established connection
func NewChannel(conn *net.TCPConn) *Channel {
	return &Channel{
		conn: conn,
		dec:  json.NewDecoder(conn),
		enc:  json.NewEncoder(conn),
	}
}

func (c *Channel) Send(msg Message) error {
	return c.enc.Encode(msg)
}

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
