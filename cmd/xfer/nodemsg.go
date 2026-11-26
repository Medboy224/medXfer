package main

import "github.com/Medboy224/medXfer/pkg/session"

// answerUnknown answers a message xfer node does not handle: error{code: unsupported} (bible
// §8.2). The peer's progress, a few times a second, it ignores: the other side wrote each answer
// to its journal (#306). A ping keeps its answer: xfer node sends no ping of its own (#335), so
// that answer is what tells the other side it is there, which does not log it.
func answerUnknown(ch *session.Channel, msg session.Message) {
	if msg.Type == session.TypeProgress {
		return
	}
	_ = ch.Send(session.Unsupported(msg.Type))
}
