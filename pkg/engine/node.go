package engine

import (
	"context"
)

// Node acts as the persistent peer-to-peer instance.
// It manages both sending and receiving across multiple sessions.
type Node struct {
	OutputDir string
	Workers   int
	ChunkSize uint32
}

func NewNode(outputDir string, workers int, chunkSize uint32) *Node {
	return &Node{
		OutputDir: outputDir,
		Workers:   workers,
		ChunkSize: chunkSize,
	}
}

// OfferFile keeps the port open for a single transfer session, then returns cleanly.
func (n *Node) OfferFile(ctx context.Context, bindAddr, filePath string, listener TransferListener) error {
	sender := NewSender(n.Workers, n.ChunkSize)
	return sender.ServeAndSend(ctx, bindAddr, filePath, listener, 0)
}

// FetchFile connects to an offering peer, downloads the file, and returns cleanly.
func (n *Node) FetchFile(ctx context.Context, targetAddr string, listener TransferListener) error {
	receiver := NewReceiver(n.OutputDir, n.Workers)
	return receiver.Pull(ctx, targetAddr, listener, "")
}
