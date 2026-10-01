package engine

import (
	"net"
	"time"
)

const (
	SocketBufferSize = 4 * 1024 * 1024 // 4 MB TCP window buffer
)

// TuneConn applies TCP optimizations to the connection.
func TuneConn(conn net.Conn) {
	if tcpConn, ok := conn.(*net.TCPConn); ok {
		_ = tcpConn.SetNoDelay(true)
		_ = tcpConn.SetKeepAlive(true)
		_ = tcpConn.SetKeepAlivePeriod(3 * time.Second)
		_ = tcpConn.SetReadBuffer(SocketBufferSize)
		_ = tcpConn.SetWriteBuffer(SocketBufferSize)
	}
}
