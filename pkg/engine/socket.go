package engine

import (
	"net"
	"time"
)

const (
	// High-performance socket buffer size (2 MB)
	DefaultSocketBuffer = 2 * 1024 * 1024
)

// TuneConn applies low-latency and high-throughput settings to a TCP socket
func TuneConn(conn net.Conn, bufSize int) error {
	tcpConn, ok := conn.(*net.TCPConn)
	if !ok {
		return nil
	}

	if bufSize <= 0 {
		bufSize = DefaultSocketBuffer
	}

	// Disable Nagle's algorithm for immediate packet dispatch
	if err := tcpConn.SetNoDelay(true); err != nil {
		return err
	}

	// Expand send and receive OS socket buffers
	if err := tcpConn.SetReadBuffer(bufSize); err != nil {
		return err
	}
	if err := tcpConn.SetWriteBuffer(bufSize); err != nil {
		return err
	}

	// Keep alive to prevent drops during large transfers
	if err := tcpConn.SetKeepAlive(true); err != nil {
		return err
	}
	if err := tcpConn.SetKeepAlivePeriod(30 * time.Second); err != nil {
		return err
	}

	return nil
}
