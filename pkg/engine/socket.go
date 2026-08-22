package engine

import (
	"net"
	"time"
)

const (
	DefaultSocketBuffer = 2 * 1024 * 1024
	SocketTimeout       = 15 * time.Second
)

func TuneConn(conn net.Conn, bufSize int) error {
	tcpConn, ok := conn.(*net.TCPConn)
	if !ok {
		return nil
	}

	if bufSize <= 0 {
		bufSize = DefaultSocketBuffer
	}

	_ = tcpConn.SetNoDelay(true)
	_ = tcpConn.SetReadBuffer(bufSize)
	_ = tcpConn.SetWriteBuffer(bufSize)
	_ = tcpConn.SetKeepAlive(true)
	_ = tcpConn.SetKeepAlivePeriod(SocketTimeout)

	return nil
}
