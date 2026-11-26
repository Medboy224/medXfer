package main

import "net"

// acceptor is what acceptEach takes connections from: the hub's control listener.
type acceptor interface {
	Accept() (net.Conn, error)
}

// acceptEach serves each connection of control in a goroutine of its own, until control is
// closed: one that waits (a PIN on demand, typed up to 2 min later) holds no other (#305). The
// session's state stays the main loop's (incomingChan).
func acceptEach(control acceptor, serve func(net.Conn)) {
	for {
		c, err := control.Accept()
		if err != nil {
			return
		}
		go serve(c)
	}
}
