package main

import (
	"bufio"
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"net"
	"os"
	"os/signal"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/Medboy224/medXfer/pkg/api"
	"github.com/Medboy224/medXfer/pkg/discovery"
	"github.com/Medboy224/medXfer/pkg/engine"
	"github.com/Medboy224/medXfer/pkg/hotspot"
	"github.com/Medboy224/medXfer/pkg/manifest"
	"github.com/Medboy224/medXfer/pkg/session"
	"github.com/Medboy224/medXfer/pkg/ui"
)

const defaultPort = 18888

var activePort = defaultPort

func isLocalIP(ipStr string) bool {
	if ipStr == "127.0.0.1" || ipStr == "localhost" {
		return true
	}
	ip := net.ParseIP(ipStr)
	if ip == nil || ip.IsLoopback() {
		return true
	}
	addrs, err := net.InterfaceAddrs()
	if err != nil {
		return false
	}
	for _, addr := range addrs {
		if ipnet, ok := addr.(*net.IPNet); ok {
			if ipnet.IP.Equal(ip) {
				return true
			}
		}
	}
	return false
}

func getLocalIP() string {
	addrs, err := net.InterfaceAddrs()
	if err != nil {
		return "127.0.0.1"
	}
	for _, a := range addrs {
		if ipnet, ok := a.(*net.IPNet); ok && !ipnet.IP.IsLoopback() {
			if ipnet.IP.To4() != nil {
				return ipnet.IP.String()
			}
		}
	}
	return "127.0.0.1"
}

type cliListener struct {
	bar      *ui.ProgressBar
	cancel   context.CancelFunc
	finished bool
	isSender bool
}

func (l *cliListener) OnStart(fileName string, fileSize int64, chunkCount uint32) {
	l.bar = ui.NewProgressBar()
	l.finished = false
}

func (l *cliListener) OnProgress(stats engine.TransferStats) {
	if l.bar != nil {
		l.bar.Render(stats.BytesTransferred, stats.TotalBytes, stats.SpeedMBps)
	}
	if l.isSender && !l.finished && stats.TotalBytes > 0 && stats.BytesTransferred >= stats.TotalBytes {
		l.finished = true
		if l.cancel != nil {
			go func() {
				time.Sleep(100 * time.Millisecond)
				fmt.Println("\n[+] Transfer completed.")
				l.cancel()
			}()
		}
	}
}

func (l *cliListener) OnChunkFailed(chunkIndex uint32, retryCount int, err error) {}

func (l *cliListener) OnComplete(savePath string, duration time.Duration) {
	if l.bar != nil {
		l.bar.Finish()
		l.bar = nil
	}
	if !l.isSender {
		fmt.Printf("\n[+] Transfer completed in %s\n", duration.Round(time.Millisecond))
	}
	if !l.finished && l.cancel != nil {
		l.finished = true
		l.cancel()
	}
}

func (l *cliListener) OnError(err error) {
	if l.bar != nil {
		l.bar.Finish()
		l.bar = nil
	}
	if !l.finished && l.cancel != nil {
		l.finished = true
		l.cancel()
	}
}

func main() {
	if len(os.Args) < 2 {
		printUsage()
		os.Exit(1)
	}
	switch os.Args[1] {
	case "daemon":
		handleDaemon(os.Args[2:])
	case "send":
		handleSend(os.Args[2:])
	case "recv":
		handleRecv(os.Args[2:])
	case "node":
		handleNode()
	default:
		printUsage()
		os.Exit(1)
	}
}

func reorderArgs(args []string) []string {
	valFlags := map[string]bool{"-port": true, "--port": true, "-workers": true, "--workers": true, "-chunk": true, "--chunk": true, "-out": true, "--out": true, "-ip": true, "--ip": true}
	var flags, positionals []string
	for i := 0; i < len(args); i++ {
		arg := args[i]
		if valFlags[arg] {
			flags = append(flags, arg)
			if i+1 < len(args) {
				i++
				flags = append(flags, args[i])
			}
		} else if strings.HasPrefix(arg, "-") {
			flags = append(flags, arg)
		} else {
			positionals = append(positionals, arg)
		}
	}
	return append(flags, positionals...)
}

func handleNode() {
	fmt.Println("==================================================")
	fmt.Println("             medXfer Persistent Node              ")
	fmt.Println("==================================================")
	fmt.Println(" Commands:")
	fmt.Println("   scan              - Find devices on the network")
	fmt.Println("   pair <index/ip>   - Connect to a device")
	fmt.Println("   send <filepath>   - Send file/folder to paired device")
	fmt.Println("   stop              - Cancel an active transfer gracefully")
	fmt.Println("   disconnect        - End pairing session")
	fmt.Println("   exit              - Shutdown node")
	fmt.Println("--------------------------------------------------")

	var activeSession *session.Channel
	var pendingOffer *session.Message
	var lastScannedPeers []discovery.Peer
	var lastOfferedFile string
	var lastOfferedRelPath string
	var lastOfferedPort int
	var lastOfferedManifest *manifest.Manifest
	var transferCancel context.CancelFunc
	itemDoneChan := make(chan bool, 1)

	// Global gracefully shutdown trap for Ctrl+C
	sigChan := make(chan os.Signal, 1)
	signal.Notify(sigChan, os.Interrupt, syscall.SIGTERM)
	go func() {
		<-sigChan
		fmt.Println("\n[*] Shutting down safely...")
		if transferCancel != nil {
			if activeSession != nil {
				activeSession.Send(session.Message{Type: "cancel"})
			}
			transferCancel()
		}
		if activeSession != nil {
			activeSession.Send(session.Message{Type: "disconnect"})
			activeSession.Close()
		}
		os.Exit(0)
	}()

	inputChan := make(chan string)
	msgChan := make(chan session.Message)
	disconnectChan := make(chan bool)

	go func() {
		scanner := bufio.NewScanner(os.Stdin)
		for scanner.Scan() {
			inputChan <- scanner.Text()
		}
	}()

	go func() {
		l, err := net.Listen("tcp4", "0.0.0.0:18887")
		if err != nil {
			return
		}
		for {
			conn, err := l.Accept()
			if err != nil {
				continue
			}
			if activeSession == nil {
				activeSession = session.NewChannel(conn.(*net.TCPConn))
				fmt.Printf("\n\n[+] Incoming pairing from %s! Press Enter to refresh prompt.\n", activeSession.RemoteIP())
				go func(c *session.Channel) {
					for {
						msg, err := c.Read()
						if err != nil {
							disconnectChan <- true
							return
						}
						msgChan <- msg
					}
				}(activeSession)
			} else {
				conn.Close()
			}
		}
	}()

	ctxDisc, cancelDisc := context.WithCancel(context.Background())
	defer cancelDisc()
	discOffer := &discovery.TransferOffer{FileName: "medXfer-Node", FileSize: 0}
	discServer := discovery.NewDiscoveryServer("node", 18887, discOffer)
	discServer.Start(ctxDisc)

	printPrompt := func() {
		if pendingOffer != nil {
			if pendingOffer.Type == "batch_offer" && pendingOffer.Batch != nil {
				fmt.Printf("\n[?] Accept folder %s? [Y/n]: ", pendingOffer.Batch.SummaryString())
			} else {
				fmt.Printf("\n[?] Accept '%s' (%.2f MB)? [Y/n]: ", pendingOffer.FileName, float64(pendingOffer.FileSize)/(1024*1024))
			}
		} else if activeSession != nil {
			fmt.Printf("\n[Paired: %s] > ", activeSession.RemoteIP())
		} else {
			fmt.Print("\nmedXfer > ")
		}
	}
	printPrompt()

	for {
		select {
		case text := <-inputChan:
			text = strings.TrimSpace(text)
			if pendingOffer != nil {
				if strings.ToLower(text) == "n" {
					activeSession.Send(session.Message{Type: "reject"})
					fmt.Println("[-] Transfer rejected.")
					pendingOffer = nil
					printPrompt()
					continue
				}

				if pendingOffer.Type == "batch_offer" {
					if transferCancel != nil {
						transferCancel()
						transferCancel = nil
					}
					fmt.Printf("[+] Batch accepted! Preparing to receive %d files...\n", pendingOffer.Batch.TotalFiles)
					activeSession.Send(session.Message{Type: "batch_accept"})
					pendingOffer = nil
					continue
				}

				// Single file transfer
				if transferCancel != nil {
					transferCancel()
					transferCancel = nil
				}

				resumeBytes, _ := engine.PeekResumeOffset(".", pendingOffer.FileName, pendingOffer.FileID, pendingOffer.FileSize, 2*1024*1024)

				if resumeBytes > 0 {
					fmt.Printf("[+] Transfer accepted. Resuming from %.2f MB (%.1f%%)...\n", float64(resumeBytes)/(1024*1024), float64(resumeBytes)/float64(pendingOffer.FileSize)*100.0)
				} else {
					fmt.Println("[+] Transfer accepted. Starting download...")
				}
				activeSession.Send(session.Message{Type: "accept", ResumeBytes: resumeBytes})

				var ctx context.Context
				ctx, transferCancel = context.WithCancel(context.Background())

				go func(ctx context.Context, ip string, port int, fileID string) {
					err := runNodeRecv(ctx, ip, port, ".", activeSession, fileID)
					if err == nil {
						activeSession.Send(session.Message{Type: "complete"})
					}
					printPrompt()
				}(ctx, activeSession.RemoteIP(), pendingOffer.DataPort, pendingOffer.FileID)

				pendingOffer = nil
				continue
			}

			if text == "" {
				printPrompt()
				continue
			}

			parts := strings.SplitN(text, " ", 2)
			cmd := strings.ToLower(parts[0])

			switch cmd {
			case "scan":
				fmt.Println("[*] Scanning local network for medXfer nodes...")
				peers, err := discovery.DiscoverPeers(2 * time.Second)
				if err != nil {
					fmt.Println("[-] Discovery error:", err)
					break
				}
				lastScannedPeers = nil
				seenIPs := make(map[string]bool)
				for _, p := range peers {
					if !isLocalIP(p.HostIP) && !seenIPs[p.HostIP] {
						seenIPs[p.HostIP] = true
						lastScannedPeers = append(lastScannedPeers, p)
					}
				}
				if len(lastScannedPeers) == 0 {
					fmt.Println("  (No other devices discovered on Wi-Fi)")
				} else {
					for i, p := range lastScannedPeers {
						fmt.Printf("  [%d] %s (%s:%d)\n", i+1, p.DeviceName, p.HostIP, p.Port)
					}
				}
			case "pair":
				if len(parts) < 2 {
					fmt.Println("[-] Usage: pair <index|IP>")
					break
				}
				targetIP := parts[1]
				if num, err := strconv.Atoi(parts[1]); err == nil && num >= 1 && num <= len(lastScannedPeers) {
					targetIP = lastScannedPeers[num-1].HostIP
				}
				fmt.Printf("[*] Connecting to node at %s:18887...\n", targetIP)
				conn, err := net.DialTimeout("tcp4", fmt.Sprintf("%s:18887", targetIP), 3*time.Second)
				if err != nil {
					fmt.Println("[-] Failed to pair:", err)
					break
				}
				activeSession = session.NewChannel(conn.(*net.TCPConn))
				fmt.Println("[+] Paired successfully!")

				go func(c *session.Channel) {
					for {
						msg, err := c.Read()
						if err != nil {
							disconnectChan <- true
							return
						}
						msgChan <- msg
					}
				}(activeSession)
			case "disconnect":
				if activeSession != nil {
					activeSession.Send(session.Message{Type: "disconnect"})
					activeSession.Close()
					activeSession = nil
					fmt.Println("[*] Disconnected.")
				}
			case "stop":
				if transferCancel != nil {
					if activeSession != nil {
						activeSession.Send(session.Message{Type: "cancel"})
					}
					transferCancel()
					transferCancel = nil
					fmt.Println("[*] Transfer stopped.")
				} else {
					fmt.Println("[-] No active transfer to stop.")
				}
			case "exit":
				if transferCancel != nil {
					if activeSession != nil {
						activeSession.Send(session.Message{Type: "cancel"})
					}
					transferCancel()
				}
				if activeSession != nil {
					activeSession.Send(session.Message{Type: "disconnect"})
					activeSession.Close()
				}
				fmt.Println("[*] Node shutdown.")
				os.Exit(0)
			case "send":
				if activeSession == nil {
					fmt.Println("[-] You must 'pair' with a device first.")
					break
				}
				if len(parts) < 2 {
					fmt.Println("[-] Usage: send <filepath or folderpath> [additional paths...]")
					break
				}
				rawArg := strings.TrimSpace(parts[1])
				rawPaths := strings.Fields(rawArg)
				if len(rawPaths) == 0 {
					rawPaths = []string{rawArg}
				}

				// Check if the target is a folder or multiple files
				isFolderOrMulti := len(rawPaths) > 1
				if len(rawPaths) == 1 {
					if info, err := os.Stat(rawPaths[0]); err == nil && info.IsDir() {
						isFolderOrMulti = true
					}
				}

				if isFolderOrMulti {
					fmt.Println("[*] Scanning directory to build manifest...")
					m, err := manifest.Build(rawPaths)
					if err != nil {
						fmt.Printf("[-] Failed to build manifest: %v\n", err)
						break
					}
					if transferCancel != nil {
						transferCancel()
						transferCancel = nil
					}
					lastOfferedManifest = m
					offer := session.Message{
						Type:  "batch_offer",
						Batch: m,
					}
					fmt.Printf("[*] Offering folder %s... Waiting for peer response.\n", m.SummaryString())
					activeSession.Send(offer)
				} else {
					filePath := rawPaths[0]
					info, err := os.Stat(filePath)
					if err != nil {
						fmt.Println("[-] Cannot read file:", err)
						break
					}

					if transferCancel != nil {
						transferCancel()
						transferCancel = nil
					}

					activePort++
					lastOfferedFile = filePath
					lastOfferedPort = activePort
					lastOfferedRelPath = ""
					lastOfferedManifest = nil
					fileID := engine.GenerateFileID(filePath)

					offer := session.Message{
						Type: "offer", FileName: filepath.Base(filePath), FileSize: info.Size(),
						FileID: fileID, DataPort: activePort,
					}
					fmt.Println("[*] Sending offer to peer... Waiting for response.")
					activeSession.Send(offer)
				}

			default:
				fmt.Println("[-] Unknown command.")
			}
			printPrompt()

		case msg := <-msgChan:
			switch msg.Type {
			case "disconnect":
				fmt.Println("\n[*] Peer disconnected.")
				if activeSession != nil {
					activeSession.Close()
					activeSession = nil
				}
				if transferCancel != nil {
					transferCancel()
					transferCancel = nil
				}
				printPrompt()

			case "offer", "batch_offer":
				if transferCancel != nil {
					transferCancel()
					transferCancel = nil
				}
				pendingOffer = &msg
				printPrompt()

			case "reject":
				fmt.Println("\n[-] Peer rejected the file transfer.")
				printPrompt()

			case "batch_accept":
				if lastOfferedManifest == nil {
					fmt.Println("[-] No active batch manifest found.")
					printPrompt()
					continue
				}
				fmt.Printf("\n[+] Peer accepted batch! Starting transfer of %s...\n", lastOfferedManifest.SummaryString())
				var ctx context.Context
				ctx, transferCancel = context.WithCancel(context.Background())

				go func(ctx context.Context, m *manifest.Manifest) {
					defer printPrompt()
					for idx, item := range m.Items {
						select {
						case <-ctx.Done():
							fmt.Println("\n[*] Batch transfer canceled.")
							return
						default:
						}

						activePort++
						port := activePort

						fmt.Printf("\n[%d/%d] Sending '%s' (%.2f MB)...\n", idx+1, m.TotalFiles, item.RelPath, float64(item.Size)/(1024*1024))

						// Clear any previous done signal
						select {
						case <-itemDoneChan:
						default:
						}

						// Start sender listener FIRST before notifying receiver
						itemCtx, itemCancel := context.WithCancel(ctx)
						go func(fPath, rPath string, p int) {
							runNodeSendWithRelPath(itemCtx, fPath, rPath, p, 4, 2*1024*1024, activeSession, 0)
						}(item.FullPath, item.RelPath, port)

						// Give listener a moment to bind
						time.Sleep(30 * time.Millisecond)

						activeSession.Send(session.Message{
							Type:      "batch_item",
							FileName:  item.RelPath,
							FileSize:  item.Size,
							FileID:    item.FileID,
							DataPort:  port,
							ItemIndex: idx,
						})

						select {
						case <-ctx.Done():
							itemCancel()
							return
						case <-itemDoneChan:
							itemCancel()
							// Item completed, advance to next
						}
					}
					fmt.Printf("\n[+] Batch transfer complete! All %d files mirrored successfully.\n", m.TotalFiles)
					activeSession.Send(session.Message{Type: "batch_complete"})
				}(ctx, lastOfferedManifest)

			case "batch_item":
				resumeBytes, _ := engine.PeekResumeOffset(".", msg.FileName, msg.FileID, msg.FileSize, 2*1024*1024)

				if msg.FileSize > 0 && resumeBytes == msg.FileSize {
					fmt.Printf("\n[%d] '%s' is already complete (skipping).\n", msg.ItemIndex+1, msg.FileName)
					activeSession.Send(session.Message{Type: "item_complete", ItemIndex: msg.ItemIndex})
					continue
				}

				if resumeBytes > 0 {
					fmt.Printf("\n[%d] Resuming '%s' from %.2f MB (%.1f%%)...\n", msg.ItemIndex+1, msg.FileName, float64(resumeBytes)/(1024*1024), float64(resumeBytes)/float64(msg.FileSize)*100.0)
				} else {
					fmt.Printf("\n[%d] Receiving '%s' (%.2f MB)...\n", msg.ItemIndex+1, msg.FileName, float64(msg.FileSize)/(1024*1024))
				}

				var itemRecvCtx context.Context
				itemRecvCtx, transferCancel = context.WithCancel(context.Background())

				go func(ctx context.Context, ip string, port int, fileID string, itemIdx int) {
					err := runNodeRecv(ctx, ip, port, ".", activeSession, fileID)
					if err == nil {
						activeSession.Send(session.Message{Type: "item_complete", ItemIndex: itemIdx})
					}
				}(itemRecvCtx, activeSession.RemoteIP(), msg.DataPort, msg.FileID, msg.ItemIndex)

			case "item_complete":
				select {
				case itemDoneChan <- true:
				default:
				}

			case "batch_complete":
				fmt.Println("\n[+] Batch transfer complete! All files mirrored successfully.")
				if transferCancel != nil {
					transferCancel()
					transferCancel = nil
				}
				printPrompt()

			case "accept":
				if msg.ResumeBytes > 0 {
					fmt.Printf("\n[+] Peer accepted! Resuming from %.2f MB... Sending...\n", float64(msg.ResumeBytes)/(1024*1024))
				} else {
					fmt.Println("\n[+] Peer accepted! Sending...")
				}
				var ctx context.Context
				ctx, transferCancel = context.WithCancel(context.Background())

				go func(offeredFile, offeredRelPath string, offeredPort int, resumeOffset int64) {
					runNodeSendWithRelPath(ctx, offeredFile, offeredRelPath, offeredPort, 4, 2*1024*1024, activeSession, resumeOffset)
					if lastOfferedManifest == nil {
						printPrompt()
					}
				}(lastOfferedFile, lastOfferedRelPath, lastOfferedPort, msg.ResumeBytes)

			case "cancel":
				fmt.Println("\n[-] Peer cancelled the transfer.")
				if transferCancel != nil {
					transferCancel()
					transferCancel = nil
				}
				printPrompt()

			case "complete":
				fmt.Println("\n[+] Receiver confirmed transfer complete.")
				if transferCancel != nil {
					transferCancel()
					transferCancel = nil
				}
				printPrompt()
			}

		case <-disconnectChan:
			if activeSession != nil {
				fmt.Println("\n[*] Connection lost.")
				activeSession.Close()
				activeSession = nil
				if transferCancel != nil {
					transferCancel()
					transferCancel = nil
				}
				printPrompt()
			}
		}
	}
}

func runNodeSend(parentCtx context.Context, filePath string, port int, workers int, chunkSize uint32, s *session.Channel, resumeOffset int64) {
	runNodeSendWithRelPath(parentCtx, filePath, "", port, workers, chunkSize, s, resumeOffset)
}

func runNodeSendWithRelPath(parentCtx context.Context, filePath, relPath string, port int, workers int, chunkSize uint32, s *session.Channel, resumeOffset int64) {
	ctx, cancel := context.WithCancel(parentCtx)
	defer cancel()
	sender := engine.NewSender(workers, chunkSize)
	listener := &cliListener{cancel: cancel, isSender: true}
	bindAddr := fmt.Sprintf("0.0.0.0:%d", port)
	_ = sender.ServeAndSendWithRelPath(ctx, bindAddr, filePath, relPath, listener, resumeOffset)
}

func runNodeRecv(parentCtx context.Context, ip string, port int, outDir string, s *session.Channel, fileID string) error {
	ctx, cancel := context.WithCancel(parentCtx)
	defer cancel()
	targetAddr := fmt.Sprintf("%s:%d", ip, port)
	receiver := engine.NewReceiver(outDir, 4)
	listener := &cliListener{cancel: cancel, isSender: false}
	return receiver.Pull(ctx, targetAddr, listener, fileID)
}

// =====================================================================
// ONE-SHOT MODE (FILES & FOLDERS)
// =====================================================================
func handleSend(args []string) {
	normalizedArgs := reorderArgs(args)
	sendCmd := flag.NewFlagSet("send", flag.ExitOnError)
	portFlag := sendCmd.Int("port", defaultPort, "TCP port to bind sender on")
	workersFlag := sendCmd.Int("workers", 4, "Number of parallel TCP streams")
	chunkSizeMB := sendCmd.Int("chunk", 2, "Chunk slice size in MB")
	createNetwork := sendCmd.Bool("create-network", false, "Create a dedicated Wi-Fi Direct network")

	_ = sendCmd.Parse(normalizedArgs)
	if sendCmd.NArg() < 1 {
		fmt.Println("Error: Missing file or folder path. Usage: xfer send <path> [additional_paths...]")
		os.Exit(1)
	}
	chunkSize := uint32(*chunkSizeMB * 1024 * 1024)
	if chunkSize == 0 {
		chunkSize = 2 * 1024 * 1024
	}

	rawPaths := sendCmd.Args()
	isFolderOrMulti := len(rawPaths) > 1
	if len(rawPaths) == 1 {
		if info, err := os.Stat(rawPaths[0]); err == nil && info.IsDir() {
			isFolderOrMulti = true
		}
	}

	if isFolderOrMulti {
		m, err := manifest.Build(rawPaths)
		if err != nil {
			fmt.Printf("[-] Failed to scan folder/files: %v\n", err)
			return
		}
		runOneShotBatchSend(m, *portFlag, *workersFlag, chunkSize, *createNetwork)
	} else {
		runOneShotSingleSend(rawPaths[0], *portFlag, *workersFlag, chunkSize, *createNetwork)
	}
}

func runOneShotBatchSend(m *manifest.Manifest, port int, workers int, chunkSize uint32, createHotspot bool) {
	var hs hotspot.Controller
	if createHotspot {
		hs = hotspot.New()
		ssid, pass := hotspot.GenerateCredentials()
		fmt.Println("[*] Creating dedicated Wi-Fi Direct network...")
		if netInfo, err := hs.Start(hotspot.Config{SSID: ssid, Password: pass, Band: hotspot.Band5GHz}); err == nil {
			defer func() { fmt.Println("\n[*] Tearing down Wi-Fi Direct network..."); _ = hs.Stop() }()
			fmt.Printf(" [NETWORK READY] %s (%s, Channel %d)\n Password: %s\n", netInfo.SSID, netInfo.Band, netInfo.Channel, netInfo.Password)
		} else {
			fmt.Printf("[-] Network Creation Failed: %v\n", err)
		}
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	sigChan := make(chan os.Signal, 1)
	signal.Notify(sigChan, os.Interrupt, syscall.SIGTERM)
	defer signal.Stop(sigChan)

	go func() {
		select {
		case <-sigChan:
			fmt.Println("\n[*] Batch transfer canceled by user.")
			cancel()
			os.Exit(0)
		case <-ctx.Done():
		}
	}()

	offer := &discovery.TransferOffer{
		FileName: m.SummaryString(),
		FileSize: m.TotalBytes,
		FileID:   m.BatchID,
		IsBatch:  true,
		Batch:    m,
	}
	discServer := discovery.NewDiscoveryServer("sender", port, offer)
	discServer.Start(ctx)

	fmt.Printf(" [OFFERING FOLDER] %s\n Listening on: %s:%d\n[*] Waiting for receiver to connect...\n", m.SummaryString(), getLocalIP(), port)

	for idx, item := range m.Items {
		if ctx.Err() != nil {
			return
		}
		itemPort := port + idx
		fmt.Printf("\n[%d/%d] Serving '%s' (%.2f MB)...\n", idx+1, m.TotalFiles, item.RelPath, float64(item.Size)/(1024*1024))

		itemCtx, itemCancel := context.WithCancel(ctx)
		sender := engine.NewSender(workers, chunkSize)
		listener := &cliListener{cancel: itemCancel, isSender: true}
		bindAddr := fmt.Sprintf("0.0.0.0:%d", itemPort)
		err := sender.ServeAndSendWithRelPath(itemCtx, bindAddr, item.FullPath, item.RelPath, listener, 0)
		itemCancel()
		if err != nil && ctx.Err() == nil {
			fmt.Printf("\n[-] Transfer error on '%s': %v\n", item.RelPath, err)
			return
		}
	}
	fmt.Printf("\n[+] Folder transfer complete! All %d files sent successfully.\n", m.TotalFiles)
}

func runOneShotSingleSend(filePath string, port int, workers int, chunkSize uint32, createHotspot bool) {
	info, err := os.Stat(filePath)
	if err != nil {
		fmt.Printf("[-] Cannot access file '%s': %v\n", filePath, err)
		return
	}

	var hs hotspot.Controller
	if createHotspot {
		hs = hotspot.New()
		ssid, pass := hotspot.GenerateCredentials()
		fmt.Println("[*] Creating dedicated Wi-Fi Direct network...")
		if netInfo, err := hs.Start(hotspot.Config{SSID: ssid, Password: pass, Band: hotspot.Band5GHz}); err == nil {
			defer func() { fmt.Println("\n[*] Tearing down Wi-Fi Direct network..."); _ = hs.Stop() }()
			fmt.Printf(" [NETWORK READY] %s (%s, Channel %d)\n Password: %s\n", netInfo.SSID, netInfo.Band, netInfo.Channel, netInfo.Password)
		} else {
			fmt.Printf("[-] Network Creation Failed: %v\n", err)
		}
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	sigChan := make(chan os.Signal, 1)
	signal.Notify(sigChan, os.Interrupt, syscall.SIGTERM)
	defer signal.Stop(sigChan)

	go func() {
		select {
		case <-sigChan:
			fmt.Println("\n[*] Transfer canceled by user.")
			cancel()
			os.Exit(0)
		case <-ctx.Done():
		}
	}()

	offer := &discovery.TransferOffer{
		FileName: filepath.Base(filePath), FileSize: info.Size(),
		FileID: engine.GenerateFileID(filePath),
	}
	discServer := discovery.NewDiscoveryServer("sender", port, offer)
	discServer.Start(ctx)
	fmt.Printf(" [OFFERING FILE] %s (%.2f MB)\n Listening on: %s:%d\n[*] Waiting for receiver to connect...\n", offer.FileName, float64(offer.FileSize)/(1024*1024), getLocalIP(), port)

	sender := engine.NewSender(workers, chunkSize)
	listener := &cliListener{cancel: cancel, isSender: true}
	bindAddr := fmt.Sprintf("0.0.0.0:%d", port)
	err = sender.ServeAndSend(ctx, bindAddr, filePath, listener, 0)
	if err != nil && ctx.Err() == nil {
		fmt.Printf("\n[-] Transfer error: %v\n", err)
	}
}

func handleRecv(args []string) {
	normalizedArgs := reorderArgs(args)
	recvCmd := flag.NewFlagSet("recv", flag.ExitOnError)
	ipFlag := recvCmd.String("ip", "", "Direct sender address")
	outDirFlag := recvCmd.String("out", ".", "Directory to save files")
	workersFlag := recvCmd.Int("workers", 4, "Number of parallel TCP streams")

	_ = recvCmd.Parse(normalizedArgs)
	targetAddr := *ipFlag
	if targetAddr != "" && !strings.Contains(targetAddr, ":") {
		targetAddr = fmt.Sprintf("%s:%d", targetAddr, defaultPort)
	}

	var selectedPeer *discovery.Peer
	if targetAddr == "" {
		selectedPeer = selectOneShotSender()
		if selectedPeer == nil {
			return
		}
		targetAddr = fmt.Sprintf("%s:%d", selectedPeer.HostIP, selectedPeer.Port)
	} else {
		host, _, _ := net.SplitHostPort(targetAddr)
		selectedPeer = fetchPeerOffer(host)
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	sigChan := make(chan os.Signal, 1)
	signal.Notify(sigChan, os.Interrupt, syscall.SIGTERM)
	defer signal.Stop(sigChan)

	go func() {
		select {
		case <-sigChan:
			fmt.Println("\n[*] Download canceled by user.")
			cancel()
			os.Exit(0)
		case <-ctx.Done():
		}
	}()

	if selectedPeer != nil && selectedPeer.Offer != nil && selectedPeer.Offer.IsBatch && selectedPeer.Offer.Batch != nil {
		runOneShotBatchRecv(ctx, targetAddr, *outDirFlag, *workersFlag, selectedPeer.Offer.Batch)
	} else {
		fileID := ""
		if selectedPeer != nil && selectedPeer.Offer != nil {
			fileID = selectedPeer.Offer.FileID
		}
		fmt.Printf("\n[*] Connecting to sender at %s...\n", targetAddr)
		receiver := engine.NewReceiver(*outDirFlag, *workersFlag)
		listener := &cliListener{cancel: cancel, isSender: false}
		err := receiver.Pull(ctx, targetAddr, listener, fileID)
		if err != nil && ctx.Err() == nil {
			fmt.Printf("\n[-] Download failed: %v\n", err)
		}
	}
}

func runOneShotBatchRecv(ctx context.Context, targetAddr, outDir string, workers int, m *manifest.Manifest) {
	fmt.Printf("\n[+] Downloading folder %s from %s...\n", m.SummaryString(), targetAddr)

	host, portStr, err := net.SplitHostPort(targetAddr)
	basePort := defaultPort
	if err == nil {
		if p, err := strconv.Atoi(portStr); err == nil {
			basePort = p
		}
	}

	for idx, item := range m.Items {
		if ctx.Err() != nil {
			return
		}
		itemPort := basePort + idx
		itemTargetAddr := fmt.Sprintf("%s:%d", host, itemPort)

		fmt.Printf("\n[%d/%d] Receiving '%s' (%.2f MB)...\n", idx+1, m.TotalFiles, item.RelPath, float64(item.Size)/(1024*1024))

		itemCtx, itemCancel := context.WithCancel(ctx)
		receiver := engine.NewReceiver(outDir, workers)
		listener := &cliListener{cancel: itemCancel, isSender: false}
		err := receiver.Pull(itemCtx, itemTargetAddr, listener, item.FileID)
		itemCancel()
		if err != nil && ctx.Err() == nil {
			fmt.Printf("\n[-] Download failed on '%s': %v\n", item.RelPath, err)
			return
		}
	}
	fmt.Printf("\n[+] Folder download complete! All %d files saved successfully.\n", m.TotalFiles)
}

func fetchPeerOffer(hostIP string) *discovery.Peer {
	conn, err := net.DialTimeout("tcp4", fmt.Sprintf("%s:%d", hostIP, discovery.DiscoveryPort), 800*time.Millisecond)
	if err != nil {
		return nil
	}
	defer conn.Close()
	data, err := io.ReadAll(conn)
	if err != nil {
		return nil
	}
	var p discovery.Peer
	if err := json.Unmarshal(data, &p); err == nil && p.Offer != nil {
		return &p
	}
	return nil
}

func selectOneShotSender() *discovery.Peer {
	reader := bufio.NewReader(os.Stdin)
	for {
		fmt.Println("\n[*] Searching local network for medXfer senders...")
		peers, err := discovery.DiscoverPeers(2 * time.Second)
		if err != nil {
			fmt.Printf("[-] Discovery error: %v\n", err)
		}
		var senders []discovery.Peer
		seenIPs := make(map[string]bool)
		for _, p := range peers {
			if p.Role == "sender" && p.Offer != nil && !isLocalIP(p.HostIP) && !seenIPs[p.HostIP] {
				seenIPs[p.HostIP] = true
				senders = append(senders, p)
			}
		}
		if len(senders) == 0 {
			fmt.Println("  (No active senders found)")
		} else {
			for i, s := range senders {
				if s.Offer.IsBatch {
					fmt.Printf("  [%d] %s (%s:%d) - Folder: %s\n", i+1, s.DeviceName, s.HostIP, s.Port, s.Offer.FileName)
				} else {
					fmt.Printf("  [%d] %s (%s:%d) - File: %s (%.2f MB)\n", i+1, s.DeviceName, s.HostIP, s.Port, s.Offer.FileName, float64(s.Offer.FileSize)/(1024*1024))
				}
			}
		}
		fmt.Print("Select an option [r to refresh, q to quit]: ")
		input, _ := reader.ReadString('\n')
		input = strings.TrimSpace(input)
		if strings.EqualFold(input, "q") {
			return nil
		}
		if strings.EqualFold(input, "r") {
			continue
		}
		if num, err := strconv.Atoi(input); err == nil && num >= 1 && num <= len(senders) {
			selected := senders[num-1]
			return &selected
		}
	}
}

func handleDaemon(args []string) {
	daemonCmd := flag.NewFlagSet("daemon", flag.ExitOnError)
	portFlag := daemonCmd.Int("port", 19999, "Port for Headless API (HTTP & WebSocket)")
	outDirFlag := daemonCmd.String("out", ".", "Default directory to save incoming files")
	nameFlag := daemonCmd.String("name", "", "Custom device name for discovery")

	_ = daemonCmd.Parse(args)

	srv := api.NewDaemonServer(*portFlag, *outDirFlag, *nameFlag)
	if err := srv.Start(*portFlag); err != nil {
		fmt.Printf("[-] Daemon error: %v\n", err)
		os.Exit(1)
	}
}

func printUsage() {
	fmt.Printf("Usage:\n  xfer daemon [--port 19999] (Headless API for Flutter GUI)\n  xfer node                 (Persistent interactive mode)\n  xfer send <file_or_folder> [more...]\n  xfer recv                 (Auto-discover senders)\n  xfer recv --ip <addr>     (Direct connect)\n")
}
