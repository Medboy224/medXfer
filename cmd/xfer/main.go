package main

import (
	"bufio"
	"context"
	"flag"
	"fmt"
	"net"
	"os"
	"os/signal"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/Medboy224/medXfer/pkg/discovery"
	"github.com/Medboy224/medXfer/pkg/engine"
	"github.com/Medboy224/medXfer/pkg/hotspot"
	"github.com/Medboy224/medXfer/pkg/session"
	"github.com/Medboy224/medXfer/pkg/ui"
)

const defaultPort = 18888

var activePort = defaultPort

// isLocalIP prevents the device from discovering itself
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
	for _, address := range addrs {
		if ipnet, ok := address.(*net.IPNet); ok && !ipnet.IP.IsLoopback() {
			if ip4 := ipnet.IP.To4(); ip4 != nil {
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
				time.Sleep(800 * time.Millisecond)
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
	case "send":
		handleSend(os.Args[2:])
	case "recv":
		handleRecv(os.Args[2:])
	case "node":
		handleNode()
	default:
		fmt.Printf("Unknown command: %s\n\n", os.Args[1])
		printUsage()
		os.Exit(1)
	}
}

func reorderArgs(args []string) []string {
	valFlags := map[string]bool{
		"-port": true, "--port": true,
		"-workers": true, "--workers": true,
		"-chunk": true, "--chunk": true,
		"-out": true, "--out": true,
		"-ip": true, "--ip": true,
	}

	var flags []string
	var positionals []string

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
	fmt.Println("   send <filepath>   - Send file to paired device")
	fmt.Println("   disconnect        - End pairing session")
	fmt.Println("   exit              - Shutdown node")
	fmt.Println("--------------------------------------------------")

	var activeSession *session.Channel
	var pendingOffer *session.Message
	var lastScannedPeers []discovery.Peer
	var lastOfferedFile string
	var lastOfferedPort int

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
			fmt.Printf("\n[?] Accept '%s' (%.2f MB)? [Y/n]: ", pendingOffer.FileName, float64(pendingOffer.FileSize)/(1024*1024))
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
				} else {
					activeSession.Send(session.Message{Type: "accept"})
					fmt.Println("[+] Transfer accepted. Starting download...")
					runNodeRecv(activeSession.RemoteIP(), pendingOffer.DataPort, ".")
				}
				pendingOffer = nil
				printPrompt()
				continue
			}

			if text == "" {
				printPrompt()
				continue
			}

			parts := strings.SplitN(text, " ", 2)
			cmd := strings.ToLower(parts[0])

			switch cmd {
			case "exit", "quit":
				fmt.Println("[*] Shutting down node...")
				if activeSession != nil {
					activeSession.Send(session.Message{Type: "disconnect"})
					activeSession.Close()
				}
				os.Exit(0)

			case "scan":
				if activeSession != nil {
					fmt.Println("[-] Disconnect first before scanning.")
					break
				}
				fmt.Println("[*] Scanning network for 2 seconds...")
				peers, _ := discovery.DiscoverPeers(2 * time.Second)
				lastScannedPeers = nil

				for _, p := range peers {
					// FIXED: Broadened filter to ensure devices are caught regardless of role string differences
					if (p.Role == "node" || p.Role == "idle" || p.Port == 18887) && !isLocalIP(p.HostIP) {
						lastScannedPeers = append(lastScannedPeers, p)
					}
				}

				if len(lastScannedPeers) == 0 {
					fmt.Println("  (No devices found)")
				} else {
					for i, p := range lastScannedPeers {
						fmt.Printf("  [%d] %s (%s)\n", i+1, p.DeviceName, p.HostIP)
					}
				}

			case "pair":
				if activeSession != nil {
					fmt.Println("[-] Already paired. Disconnect first.")
					break
				}
				if len(parts) < 2 {
					fmt.Println("[-] Usage: pair <index or ip>")
					break
				}
				target := parts[1]

				if num, err := strconv.Atoi(target); err == nil && num >= 1 && num <= len(lastScannedPeers) {
					target = lastScannedPeers[num-1].HostIP + ":18887"
				} else if !strings.Contains(target, ":") {
					target += ":18887"
				}

				conn, err := net.Dial("tcp4", target)
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

			case "send":
				if activeSession == nil {
					fmt.Println("[-] You must 'pair' with a device first.")
					break
				}
				if len(parts) < 2 {
					fmt.Println("[-] Usage: send <filepath>")
					break
				}
				filePath := parts[1]
				info, err := os.Stat(filePath)
				if err != nil {
					fmt.Println("[-] Cannot read file:", err)
					break
				}

				activePort++
				lastOfferedFile = filePath
				lastOfferedPort = activePort

				offer := session.Message{
					Type:     "offer",
					FileName: filepath.Base(filePath),
					FileSize: info.Size(),
					DataPort: activePort,
				}
				fmt.Println("[*] Sending offer to peer... Waiting for response.")
				activeSession.Send(offer)

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
				printPrompt()
			case "offer":
				pendingOffer = &msg
				printPrompt()
			case "reject":
				fmt.Println("\n[-] Peer rejected the file transfer.")
				printPrompt()
			case "accept":
				fmt.Println("\n[+] Peer accepted! Sending...")
				runNodeSend(lastOfferedFile, lastOfferedPort, 4, 2*1024*1024)
				printPrompt()
			}

		case <-disconnectChan:
			if activeSession != nil {
				fmt.Println("\n[*] Connection lost.")
				activeSession.Close()
				activeSession = nil
				printPrompt()
			}
		}
	}
}

func runNodeSend(filePath string, port int, workers int, chunkSize uint32) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	sigChan := make(chan os.Signal, 1)
	signal.Notify(sigChan, os.Interrupt, syscall.SIGTERM)
	defer signal.Stop(sigChan)

	go func() {
		select {
		case <-sigChan:
			cancel()
		case <-ctx.Done():
		}
	}()

	sender := engine.NewSender(workers, chunkSize)
	listener := &cliListener{cancel: cancel, isSender: true}
	bindAddr := fmt.Sprintf("0.0.0.0:%d", port)

	_ = sender.ServeAndSend(ctx, bindAddr, filePath, listener)
}

func runNodeRecv(ip string, port int, outDir string) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	sigChan := make(chan os.Signal, 1)
	signal.Notify(sigChan, os.Interrupt, syscall.SIGTERM)
	defer signal.Stop(sigChan)

	go func() {
		select {
		case <-sigChan:
			cancel()
		case <-ctx.Done():
		}
	}()

	targetAddr := fmt.Sprintf("%s:%d", ip, port)
	receiver := engine.NewReceiver(outDir, 4)
	listener := &cliListener{cancel: cancel, isSender: false}

	_ = receiver.Pull(ctx, targetAddr, listener)
}

func handleSend(args []string) {
	normalizedArgs := reorderArgs(args)
	sendCmd := flag.NewFlagSet("send", flag.ExitOnError)
	portFlag := sendCmd.Int("port", defaultPort, "TCP port to bind sender on")
	workersFlag := sendCmd.Int("workers", 4, "Number of parallel TCP streams")
	chunkSizeMB := sendCmd.Int("chunk", 2, "Chunk slice size in MB")
	createNetwork := sendCmd.Bool("create-network", false, "Create a dedicated Wi-Fi Direct network")

	_ = sendCmd.Parse(normalizedArgs)

	if sendCmd.NArg() < 1 {
		fmt.Println("Error: Missing file path. Usage: xfer send <file>")
		os.Exit(1)
	}

	chunkSize := uint32(*chunkSizeMB * 1024 * 1024)
	if chunkSize == 0 {
		chunkSize = 2 * 1024 * 1024
	}

	runOneShotSend(sendCmd.Arg(0), *portFlag, *workersFlag, chunkSize, *createNetwork)
}

func runOneShotSend(filePath string, port int, workers int, chunkSize uint32, createHotspot bool) {
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
			defer func() {
				fmt.Println("\n[*] Tearing down Wi-Fi Direct network...")
				_ = hs.Stop()
			}()
			fmt.Println("==================================================")
			fmt.Printf(" [NETWORK READY] %s (%s, Channel %d)\n", netInfo.SSID, netInfo.Band, netInfo.Channel)
			fmt.Printf(" Password: %s\n", netInfo.Password)
			fmt.Println("==================================================")
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

	offer := &discovery.TransferOffer{FileName: filepath.Base(filePath), FileSize: info.Size()}
	discServer := discovery.NewDiscoveryServer("sender", port, offer)
	discServer.Start(ctx)

	fmt.Println("==================================================")
	fmt.Printf(" [OFFERING FILE] %s (%.2f MB)\n", offer.FileName, float64(offer.FileSize)/(1024*1024))
	fmt.Println("==================================================")

	localIP := getLocalIP()
	fmt.Printf(" Listening on: %s:%d\n", localIP, port)
	fmt.Println("[*] Waiting for receiver to connect...")

	sender := engine.NewSender(workers, chunkSize)
	listener := &cliListener{cancel: cancel, isSender: true}
	bindAddr := fmt.Sprintf("0.0.0.0:%d", port)

	err = sender.ServeAndSend(ctx, bindAddr, filePath, listener)
	if err != nil {
		if ctx.Err() != nil {
			return
		}
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

	if targetAddr == "" {
		targetAddr = selectOneShotSender()
		if targetAddr == "" {
			return
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
			fmt.Println("\n[*] Download canceled by user.")
			cancel()
			os.Exit(0)
		case <-ctx.Done():
		}
	}()

	fmt.Printf("\n[*] Connecting to sender at %s...\n", targetAddr)
	receiver := engine.NewReceiver(*outDirFlag, *workersFlag)
	listener := &cliListener{cancel: cancel, isSender: false}

	err := receiver.Pull(ctx, targetAddr, listener)
	if err != nil && ctx.Err() == nil {
		fmt.Printf("\n[-] Download failed: %v\n", err)
	}
}

func selectOneShotSender() string {
	reader := bufio.NewReader(os.Stdin)
	for {
		fmt.Println("\n[*] Searching local network for medXfer senders...")
		peers, err := discovery.DiscoverPeers(2 * time.Second)
		if err != nil {
			fmt.Printf("[-] Discovery error: %v\n", err)
		}

		var senders []discovery.Peer
		for _, p := range peers {
			if p.Role == "sender" && p.Offer != nil && !isLocalIP(p.HostIP) {
				senders = append(senders, p)
			}
		}

		fmt.Println("==================================================")
		fmt.Println("       medXfer - Available Senders on LAN         ")
		fmt.Println("==================================================")

		if len(senders) == 0 {
			fmt.Println(" (No active senders found)")
		} else {
			for i, s := range senders {
				sizeMB := float64(s.Offer.FileSize) / (1024 * 1024)
				fmt.Printf(" [%d] %s (%s:%d)\n", i+1, s.DeviceName, s.HostIP, s.Port)
				fmt.Printf("     File: %s (%.2f MB)\n\n", s.Offer.FileName, sizeMB)
			}
		}

		fmt.Println("--------------------------------------------------")
		fmt.Println(" [r] Scan again (Refresh)")
		fmt.Println(" [m] Enter Sender IP manually")
		fmt.Println(" [q] Quit / Cancel")
		fmt.Println("--------------------------------------------------")
		fmt.Print("Select an option: ")

		input, _ := reader.ReadString('\n')
		input = strings.TrimSpace(input)

		if strings.EqualFold(input, "q") {
			return ""
		} else if strings.EqualFold(input, "r") {
			continue
		} else if strings.EqualFold(input, "m") {
			fmt.Print("Enter sender IP:Port: ")
			manualInput, _ := reader.ReadString('\n')
			manualInput = strings.TrimSpace(manualInput)
			if !strings.Contains(manualInput, ":") {
				manualInput = fmt.Sprintf("%s:%d", manualInput, defaultPort)
			}
			return manualInput
		} else if num, err := strconv.Atoi(input); err == nil && num >= 1 && num <= len(senders) {
			selected := senders[num-1]
			fmt.Printf("\n[+] Selected [%s] offering '%s'\n", selected.DeviceName, selected.Offer.FileName)
			return fmt.Sprintf("%s:%d", selected.HostIP, selected.Port)
		} else {
			fmt.Println("[-] Invalid selection.")
		}
	}
}

func printUsage() {
	fmt.Printf("medXfer - High-Speed Peer-to-Peer CLI File Transfer\n\n")
	fmt.Printf("Usage:\n")
	fmt.Printf("  xfer node                 (Persistent pairing mode)\n")
	fmt.Printf("  xfer send <file_path>     (One-shot send mode)\n")
	fmt.Printf("  xfer recv                 (One-shot receive mode)\n\n")
}
