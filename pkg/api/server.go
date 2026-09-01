package api

import (
	"context"
	"fmt"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"time"

	"github.com/Medboy224/medXfer/pkg/discovery"
	"github.com/Medboy224/medXfer/pkg/engine"
	"github.com/gorilla/websocket"
)

var upgrader = websocket.Upgrader{
	CheckOrigin: func(r *http.Request) bool {
		return true // Allow localhost connections from Flutter
	},
}

type Message struct {
	Action  string      `json:"action"`
	Payload interface{} `json:"payload"`
}

// wsListener translates engine events into JSON WebSocket messages
type wsListener struct {
	conn *websocket.Conn
}

func (w *wsListener) send(action string, payload interface{}) {
	_ = w.conn.WriteJSON(Message{Action: action, Payload: payload})
}

func (w *wsListener) OnStart(fileName string, fileSize int64, chunkCount uint32) {
	w.send("start", map[string]interface{}{
		"file_name": fileName,
		"file_size": fileSize,
	})
}

func (w *wsListener) OnProgress(stats engine.TransferStats) {
	w.send("progress", stats)
}

func (w *wsListener) OnChunkFailed(chunkIndex uint32, retryCount int, err error) {
	w.send("error", fmt.Sprintf("Chunk %d failed: %v", chunkIndex, err))
}

func (w *wsListener) OnComplete(savePath string, duration time.Duration) {
	w.send("complete", map[string]interface{}{
		"save_path":   savePath,
		"duration_ms": duration.Milliseconds(),
	})
}

func (w *wsListener) OnError(err error) {
	w.send("error", err.Error())
}

// StartServer boots the headless daemon on a dedicated local port
func StartServer(port int) {
	http.HandleFunc("/ws", handleWebSocket)
	addr := fmt.Sprintf("127.0.0.1:%d", port)
	log.Printf("[*] medXfer Headless API running on ws://%s/ws", addr)
	log.Fatal(http.ListenAndServe(addr, nil))
}

func handleWebSocket(w http.ResponseWriter, r *http.Request) {
	conn, err := upgrader.Upgrade(w, r, nil)
	if err != nil {
		return
	}
	defer conn.Close()

	listener := &wsListener{conn: conn}
	var activeCtx context.Context
	var cancelTransfer context.CancelFunc
	activePort := 18888

	for {
		var msg Message
		if err := conn.ReadJSON(&msg); err != nil {
			if cancelTransfer != nil {
				cancelTransfer()
			}
			break // Client disconnected
		}

		switch msg.Action {
		case "scan":
			go func() {
				listener.send("status", "Scanning network...")
				peers, _ := discovery.DiscoverPeers(2 * time.Second)
				listener.send("peers_found", peers)
			}()

		case "send":
			filePath, ok := msg.Payload.(string)
			if !ok {
				continue
			}
			if cancelTransfer != nil {
				cancelTransfer()
			}
			activeCtx, cancelTransfer = context.WithCancel(context.Background())
			activePort++ // Increment port to avoid lingering TIME_WAIT conflicts

			go startSending(activeCtx, filePath, activePort, listener)

		case "recv":
			data, ok := msg.Payload.(map[string]interface{})
			if !ok {
				continue
			}
			targetAddr := data["ip"].(string)
			outDir := data["out_dir"].(string)

			if cancelTransfer != nil {
				cancelTransfer()
			}
			activeCtx, cancelTransfer = context.WithCancel(context.Background())

			go startReceiving(activeCtx, targetAddr, outDir, listener)

		case "cancel":
			if cancelTransfer != nil {
				cancelTransfer()
				listener.send("canceled", nil)
			}
		}
	}
}

func startSending(ctx context.Context, filePath string, port int, listener *wsListener) {
	info, err := os.Stat(filePath)
	if err != nil {
		listener.OnError(err)
		return
	}

	offer := &discovery.TransferOffer{FileName: filepath.Base(filePath), FileSize: info.Size()}
	discServer := discovery.NewDiscoveryServer("sender", port, offer)
	discServer.Start(ctx)

	sender := engine.NewSender(4, 2*1024*1024)
	bindAddr := fmt.Sprintf("0.0.0.0:%d", port)

	err = sender.ServeAndSend(ctx, bindAddr, filePath, listener)
	if err != nil && ctx.Err() == nil {
		listener.OnError(err)
	}
}

func startReceiving(ctx context.Context, targetAddr, outDir string, listener *wsListener) {
	receiver := engine.NewReceiver(outDir, 4)
	err := receiver.Pull(ctx, targetAddr, listener)
	if err != nil && ctx.Err() == nil {
		listener.OnError(err)
	}
}
