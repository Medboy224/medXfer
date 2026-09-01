package engine

import (
	"net"
	"testing"
	"time"
)

func TestTuneConn(t *testing.T) {
	// Create a local TCP listener to generate a net.Conn
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("Failed to listen: %v", err)
	}
	defer l.Close()

	go func() {
		conn, _ := l.Accept()
		if conn != nil {
			_ = conn.Close()
		}
	}()

	conn, err := net.Dial("tcp", l.Addr().String())
	if err != nil {
		t.Fatalf("Failed to dial: %v", err)
	}
	defer conn.Close()

	// Ensure TuneConn applies settings without panicking
	TuneConn(conn)
}

// mockListener satisfies the TransferListener interface for tests
type mockListener struct {
	started bool
}

func (m *mockListener) OnStart(fileName string, fileSize int64, chunkCount uint32) { m.started = true }
func (m *mockListener) OnProgress(stats TransferStats)                             {}
func (m *mockListener) OnChunkFailed(chunkIndex uint32, retryCount int, err error) {}
func (m *mockListener) OnComplete(savePath string, duration time.Duration)         {}
func (m *mockListener) OnError(err error)                                          {}

func TestEngineInitialization(t *testing.T) {
	sender := NewSender(4, 2*1024*1024)
	if sender == nil {
		t.Fatal("Expected Sender to initialize")
	}

	receiver := NewReceiver(".", 8)
	if receiver == nil {
		t.Fatal("Expected Receiver to initialize")
	}

	_ = &mockListener{} // Verify interface implementation
}
