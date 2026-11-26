package main

import "github.com/Medboy224/medXfer/pkg/session"

// answerUnknown answers a message xfer node does not handle: error{code: unsupported} (bible
// §8.2). The peer's progress and pings, a few times a second, it ignores: the other side wrote
// each answer to its journal (#306).
func answerUnknown(ch *session.Channel, msg session.Message) {
	switch msg.Type {
	case session.TypeProgress, session.TypePing:
		return
	}
	_ = ch.Send(session.Unsupported(msg.Type))
}
