package main

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/Medboy224/medXfer/pkg/api"
	"github.com/Medboy224/medXfer/pkg/diag"
	"github.com/Medboy224/medXfer/pkg/discovery"
	"github.com/Medboy224/medXfer/pkg/engine"
	"github.com/Medboy224/medXfer/pkg/hotspot"
	"github.com/Medboy224/medXfer/pkg/identity"
	"github.com/Medboy224/medXfer/pkg/manifest"
	"github.com/Medboy224/medXfer/pkg/pairing"
	"github.com/Medboy224/medXfer/pkg/protocol"
	"github.com/Medboy224/medXfer/pkg/securechan"
	"github.com/Medboy224/medXfer/pkg/session"
	"github.com/Medboy224/medXfer/pkg/transfer"
	"github.com/Medboy224/medXfer/pkg/ui"
)

const defaultPort = 18888

// nodePort is the single port of xfer node: pairing, session and data (CHAN-01).
const nodePort = 18887

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
	return discovery.GetPrimaryLocalIP()
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
	// The sender counts bytes written to its sockets, not bytes received: at 100 % the end of
	// the file may still sit in the send buffers. It is done once the receiver, which closes
	// its connections after finalizing the file, has gone (#44).
	if l.isSender && !l.finished && stats.TotalBytes > 0 && stats.BytesTransferred >= stats.TotalBytes &&
		stats.ActiveStreams == 0 {
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

// normalizeCommand tolerates how the subcommand reaches us from Termux and Windows scripts:
// surrounding quotes, stray whitespace, invisible characters picked up by copy-paste
// (U+00A0 no-break space, U+FEFF byte-order mark), leading dashes ("--daemon") and case.
func normalizeCommand(arg string) string {
	arg = strings.Trim(arg, " \t\r\n\"'\u00a0\ufeff")
	arg = strings.TrimLeft(arg, "-")
	return strings.ToLower(arg)
}

// getEffectiveArgs returns the arguments after the program name. On Android/Termux, depending
// on how the binary is started (wrapper script, launch through the dynamic loader), argv[1] can
// repeat the program path, so "xfer daemon" arrived as ["xfer", "xfer", "daemon"] and was
// rejected as an unknown command (commit c9cdc2f). A first argument naming the program itself
// is dropped.
func getEffectiveArgs() []string {
	args := os.Args[1:]
	if len(args) > 0 {
		base0 := filepath.Base(os.Args[0])
		base1 := filepath.Base(args[0])
		if args[0] == os.Args[0] || base0 == base1 || filepath.Clean(args[0]) == filepath.Clean(os.Args[0]) {
			args = args[1:]
		}
	}
	return args
}

func main() {
	diag.AppVersion = discovery.AppVersion
	args := getEffectiveArgs()
	if len(args) < 1 {
		printUsage()
		os.Exit(0)
	}
	cmd := normalizeCommand(args[0])
	switch cmd {
	case "daemon", "d", "server", "srv":
		handleDaemon(args[1:])
	case "dev-ui", "devui":
		if !api.DashboardAvailable {
			fmt.Fprintln(os.Stderr, "[-] xfer dev-ui: the web dashboard is a development tool, not part of this build.\n"+
				"    Build with: go build -tags devui -o xfer ./cmd/xfer   (build.bat does)")
			os.Exit(1)
		}
		handleDaemon(args[1:])
	case "share", "web":
		handleShare(args[1:])
	case "send", "s":
		handleSend(args[1:])
	case "recv", "receive", "r":
		handleRecv(args[1:])
	case "node", "n", "cli":
		handleNode()
	case "report":
		handleReport(args[1:])
	case "ctl":
		handleCtl(args[1:])
	case "bench":
		handleBench(args[1:])
	case "bundle":
		handleBundle(args[1:])
	case "id":
		handleID(args[1:])
	case "trust":
		handleTrust(args[1:])
	case "pair":
		handlePair(args[1:])
	case "help", "h", "?":
		printUsage()
		os.Exit(0)
	default:
		fmt.Printf("[-] Commande inconnue : '%s'\n", args[0])
		if len(args) > 1 {
			fmt.Printf("    Arguments reçus : %v\n", args[1:])
		}
		fmt.Println()
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
		"-code": true, "--code": true,
		"-pin": true, "--pin": true,
		"-name": true, "--name": true,
	}
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
	nodePairingCode, _, _ := discovery.GeneratePairingCode(getLocalIP())

	ctxDisc, cancelDisc := context.WithCancel(context.Background())
	defer cancelDisc()
	discOffer := &discovery.TransferOffer{FileName: "medXfer-Node", FileSize: 0}
	discServer := discovery.NewDiscoveryServer("node", 18887, discOffer)
	discServer.SetPairingCode(nodePairingCode)
	discServer.Start(ctxDisc)

	nodeID, nodeTrust, err := loadIdentity(configDir())
	if err != nil {
		fmt.Fprintf(os.Stderr, "[-] %v\n", err)
		return
	}
	// Control connections: unknown keys start UNAUTH. The pairing code only finds this node on
	// the network; trust comes from the key (WP-3.6b): trusted, QR code secret, or the user.
	pairCfg := securechan.Config{Identity: nodeID, Trust: nodeTrust, AllowUnknown: true, Gate: securechan.NewGate()}
	localID = nodeID
	var invite nodeInvitation // the QR code shown by `pair --show`
	// The node's single port: control connections to the loop below, data connections to the
	// transfers registered with their peer's key (CHAN-01, CHAN-06).
	nodeHub := transfer.NewHub(pairCfg)
	nodeHub.Wrap = engine.WrapAccepted

	fmt.Println("==================================================")
	fmt.Println("             medXfer Persistent Node              ")
	fmt.Println("==================================================")
	fmt.Printf(" Device Name : %s\n", discServer.GetPeer().DeviceName)
	fmt.Printf(" Local IP    : %s\n", getLocalIP())
	fmt.Printf(" Find code   : %s   (locates this node; trust comes from its key)\n", nodePairingCode)
	fmt.Printf(" Fingerprint : %s\n", nodeID.Fingerprint.Short())
	fmt.Println("--------------------------------------------------")
	fmt.Println(" Commands:")
	fmt.Println("   scan              - Find devices on the network")
	fmt.Println("   pair <code|n|ip>  - Connect; both screens compare fingerprints")
	fmt.Println("   pair --show       - Show a QR code for the other device [--once]")
	fmt.Println("   pair <medxfer://> - Pair with the device that shows a QR code [--once]")
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
	var lastOfferedListener *transfer.Listener // data connections of the file last offered
	var lastOfferedManifest *manifest.Manifest
	var transferCancel context.CancelFunc
	var printPrompt func()
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
	trustReqChan := make(chan *trustRequest) // incoming unknown keys, decided in the command loop
	var pendingTrust *trustRequest
	disconnectChan := make(chan bool)

	go func() {
		scanner := bufio.NewScanner(os.Stdin)
		for scanner.Scan() {
			inputChan <- scanner.Text()
		}
	}()

	go func() {
		l, err := net.Listen("tcp4", fmt.Sprintf("0.0.0.0:%d", nodePort))
		if err != nil {
			return
		}
		control := nodeHub.Control()
		go func() { _ = nodeHub.Serve(l) }()
		for {
			c, err := control.Accept()
			if err != nil {
				return
			}
			conn := c.(*securechan.Conn) // already secure: the hub ran the handshake

			ch := session.NewChannel(conn)

			if activeSession != nil {
				_ = ch.Send(session.Message{Type: "pair_reject", FileName: "node already paired with another device"})
				ch.Close()
				continue
			}

			// Read incoming pairing authentication message (5s timeout)
			_ = conn.SetReadDeadline(time.Now().Add(5 * time.Second))
			msg, err := ch.Read()
			_ = conn.SetReadDeadline(time.Time{})
			if err != nil {
				ch.Close()
				continue
			}

			if msg.Type != "pair_request" {
				_ = ch.Send(session.Message{Type: "pair_reject", FileName: "pair_request expected"})
				ch.Close()
				continue
			}

			// ID-05: a trusted device's address presenting another key is refused.
			peerFP, peerIP := ch.PeerFingerprint(), ch.RemoteIP()
			if known, changed := keyChanged(nodeTrust, peerIP, peerFP); changed {
				_ = ch.Send(session.Message{Type: "pair_reject", FileName: "device key changed"})
				ch.Close()
				fmt.Printf("\n%s\n", keyChangedAlert(known, peerIP, peerFP))
				printPrompt()
				continue
			}
			// The pairing code, if any, is ignored (WP-3.6b). A QR code's secret admits its
			// scanner; otherwise an unknown key is trusted only if the user says so (WP-3.3).
			answer := trustOnce
			if msg.Secret != "" {
				var ok bool
				if answer, ok = invite.redeem(msg.Secret); !ok {
					_ = ch.Send(session.Message{Type: "pair_reject", FileName: pairing.ErrRefused.Error()}) // PAIR-07
					ch.Close()
					fmt.Printf("\n[-] Pairing attempt from %s refused: wrong or expired QR code.\n", peerIP)
					printPrompt()
					continue
				}
			} else if conn.State() == securechan.Unauth {
				req := &trustRequest{name: msg.DeviceName, ip: peerIP, fp: peerFP,
					expires: time.Now().Add(trustPromptTimeout), reply: make(chan trustAnswer, 1)}
				answer = trustRefused
				select {
				case trustReqChan <- req:
					select {
					case answer = <-req.reply:
					case <-time.After(trustPromptTimeout):
					}
				case <-time.After(trustPromptTimeout):
				}
				if answer == trustRefused {
					_ = ch.Send(session.Message{Type: "pair_reject", FileName: "device not trusted"})
					ch.Close()
					fmt.Printf("\n[-] Pairing with %s refused: device not trusted.\n", peerIP)
					printPrompt()
					continue
				}
			}
			if err := rememberPeer(nodeTrust, peerFP, msg.DeviceName, peerIP, answer); err != nil {
				fmt.Printf("\n[-] Could not save the trusted device: %v\n", err)
			}

			ch.Authenticate()
			_ = ch.Send(session.Message{
				Type:       "pair_accept",
				DeviceName: discServer.GetPeer().DeviceName,
			})
			activeSession = ch
			devLabel := msg.DeviceName
			if devLabel == "" {
				devLabel = "Remote Node"
			}
			fmt.Printf("\n\n[+] Device '%s' (%s) paired, key %s. Press Enter to refresh prompt.\n", devLabel, activeSession.RemoteIP(), peerFP.Short())

			go func(c *session.Channel) {
				for {
					m, err := c.Read()
					if err != nil {
						disconnectChan <- true
						return
					}
					msgChan <- m
				}
			}(activeSession)
		}
	}()

	printPrompt = func() {
		if pendingTrust != nil {
			fmt.Print("\n[?] Trust this device? [y]es for this session / [a]lways / [N]o: ")
			return
		}
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
		case req := <-trustReqChan:
			pendingTrust = req
			fmt.Printf("\n\n%s", trustQuestion(req.name, req.fp, nodeID.Fingerprint))
			continue
		case text := <-inputChan:
			text = strings.TrimSpace(text)
			if pendingTrust != nil {
				req := pendingTrust
				pendingTrust = nil
				if time.Now().After(req.expires) {
					fmt.Println("[-] Too late: the pairing request has expired.")
				} else {
					answer := parseTrustAnswer(text)
					req.reply <- answer
					if answer == trustRefused {
						fmt.Println("[-] Device not trusted.")
					}
				}
				printPrompt()
				continue
			}
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

				go func(ctx context.Context, ip string, port int, fileID, transferID string) {
					err := runNodeRecv(ctx, ip, port, ".", activeSession, fileID, transferID)
					if err == nil {
						activeSession.Send(session.Message{Type: "complete"})
					}
					printPrompt()
				}(ctx, activeSession.RemoteIP(), pendingOffer.DataPort, pendingOffer.FileID, pendingOffer.TransferID)

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
						codeStr := ""
						if p.PairingCode != "" {
							codeStr = fmt.Sprintf(" [Code: %s]", p.PairingCode)
						}
						fmt.Printf("  [%d] %s (%s:%d)%s\n", i+1, p.DeviceName, p.HostIP, p.Port, codeStr)
					}
				}
			case "pair":
				args := []string{}
				if len(parts) == 2 {
					args = strings.Fields(parts[1])
				}
				once := false
				var rawArg string
				show := false
				for _, a := range args {
					switch a {
					case "--once":
						once = true
					case "--show":
						show = true
					default:
						rawArg = strings.Trim(a, `'"`) // a URI pasted with its quotes
					}
				}
				if show {
					u, err := invite.renew(nodeID.Fingerprint, getLocalIP(), 18887, once)
					if err != nil {
						fmt.Printf("[-] %v\n", err)
						break
					}
					fmt.Println(" On the other device, scan this QR code or run there:")
					fmt.Printf("   xfer pair '%s'   (or, in xfer node: pair <uri>)\n\n", u)
					discovery.PrintTerminalQR(u.String())
					fmt.Printf(" This device: %s   (valid %d s, for one device)\n", nodeID.Fingerprint.Short(), int(pairing.TTL.Seconds()))
					break
				}
				if rawArg == "" {
					fmt.Println("[-] Usage: pair <6-digit code | index | IP | medxfer://...> [--once], or pair --show [--once]")
					break
				}
				targetIP := rawArg
				targetPort := 18887
				targetDeviceName := ""
				var qr *pairing.URI

				if strings.HasPrefix(rawArg, "medxfer://") {
					u, err := pairing.Parse(rawArg, time.Now())
					if err != nil {
						fmt.Printf("[-] %v\n", err)
						break
					}
					qr = &u
					targetIP, targetPort = u.Host, u.Port
				} else if normCode, err := discovery.NormalizePairingCode(rawArg); err == nil {
					// The code only finds the device; its key is checked below.
					fmt.Printf("[*] Searching for device with code '%s'...\n", normCode)
					ctxPair, cancelPair := context.WithTimeout(context.Background(), 4*time.Second)
					peer, err := discovery.ResolvePairingCode(ctxPair, normCode, 4*time.Second)
					cancelPair()
					if err != nil {
						fmt.Printf("[-] Failed to find device: %v\n", err)
						break
					}
					targetIP = peer.HostIP
					targetDeviceName = peer.DeviceName
					if peer.Port > 0 {
						targetPort = peer.Port
					}
					fmt.Printf("[+] Located node '%s' at %s:%d\n", peer.DeviceName, targetIP, targetPort)
				} else if num, err := strconv.Atoi(rawArg); err == nil && num >= 1 && num <= len(lastScannedPeers) {
					targetIP = lastScannedPeers[num-1].HostIP
					targetDeviceName = lastScannedPeers[num-1].DeviceName
					if lastScannedPeers[num-1].Port > 0 {
						targetPort = lastScannedPeers[num-1].Port
					}
				}

				if targetIP == "" {
					fmt.Println("[-] Target address could not be resolved.")
					break
				}

				fmt.Printf("[*] Connecting to node at %s:%d...\n", targetIP, targetPort)
				dialCfg := pairCfg
				if qr != nil {
					dialCfg.Expected, dialCfg.AllowUnknown = qr.Fingerprint, false // the QR code's key
				}
				conn, err := dialSecure(net.JoinHostPort(targetIP, strconv.Itoa(int(targetPort))), dialCfg)
				if err != nil {
					fmt.Println("[-] Failed to connect:", err)
					break
				}

				peerFP := conn.PeerFingerprint()
				if known, changed := keyChanged(nodeTrust, targetIP, peerFP); changed && qr == nil {
					fmt.Println(keyChangedAlert(known, targetIP, peerFP))
					conn.Close()
					break
				}

				ch := session.NewChannel(conn)

				req := session.Message{Type: "pair_request", DeviceName: discServer.GetPeer().DeviceName}
				if qr != nil {
					req.Secret = pairing.EncodeSecret(qr.Secret) // proves we saw the QR code
				}
				_ = ch.Send(req)

				// Wait for the answer: the other user may be asked to trust this device.
				_ = conn.SetReadDeadline(time.Now().Add(trustPromptTimeout + 10*time.Second))
				resp, err := ch.Read()
				_ = conn.SetReadDeadline(time.Time{})

				if err != nil || (resp.Type != "pair_accept" && resp.Type != "pair_hello_ack") {
					reason := "refused by the other device"
					if err != nil {
						reason = err.Error()
					}
					if resp.FileName != "" {
						reason = resp.FileName
					}
					fmt.Printf("[-] Pairing rejected: %s\n", reason)
					ch.Close()
					break
				}

				remoteName := resp.DeviceName
				if remoteName == "" {
					remoteName = targetDeviceName
				}
				// The QR code's key is trusted; otherwise an unknown key is trusted only if our
				// user says so (WP-3.3).
				answer := trustOnce
				if qr != nil {
					if !once {
						answer = trustAlways
					}
				} else if conn.State() == securechan.Unauth {
					fmt.Print(trustQuestion(remoteName, peerFP, nodeID.Fingerprint))
					answer = trustRefused
					select {
					case reply := <-inputChan:
						answer = parseTrustAnswer(reply)
					case <-time.After(trustPromptTimeout):
						fmt.Println()
					}
					if answer == trustRefused {
						fmt.Println("[-] Pairing cancelled: device not trusted.")
						ch.Close()
						break
					}
				}
				if err := rememberPeer(nodeTrust, peerFP, remoteName, targetIP, answer); err != nil {
					fmt.Printf("[-] Could not save the trusted device: %v\n", err)
				}
				ch.Authenticate()
				activeSession = ch
				if remoteName == "" {
					remoteName = targetIP
				}
				fmt.Printf("[+] Paired successfully with '%s' (%s)!\n", remoteName, targetIP)

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

					// Joinable from the offer on: the receiver's first DATA_JOIN finds it.
					tid, tl, err := registerNodeTransfer(nodeHub, activeSession)
					if err != nil {
						fmt.Printf("[-] %v\n", err)
						break
					}
					if lastOfferedListener != nil {
						lastOfferedListener.Close()
					}
					lastOfferedFile = filePath
					lastOfferedListener = tl
					lastOfferedRelPath = ""
					lastOfferedManifest = nil
					fileID := engine.GenerateFileID(filePath)

					offer := session.Message{
						Type: "offer", FileName: filepath.Base(filePath), FileSize: info.Size(),
						FileID: fileID, DataPort: nodePort, TransferID: tid.String(),
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
				if lastOfferedListener != nil {
					lastOfferedListener.Close() // nobody may join a refused offer
					lastOfferedListener = nil
				}
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

						fmt.Printf("\n[%d/%d] Sending '%s' (%.2f MB)...\n", idx+1, m.TotalFiles, item.RelPath, float64(item.Size)/(1024*1024))

						// Clear any previous done signal
						select {
						case <-itemDoneChan:
						default:
						}

						// Register the item FIRST: the receiver joins it as soon as it hears of it.
						itemCtx, itemCancel := context.WithCancel(ctx)
						tid, tl, err := registerNodeTransfer(nodeHub, activeSession)
						if err != nil {
							fmt.Printf("\n[-] Failed to serve '%s': %v (skipping)\n", item.RelPath, err)
							itemCancel()
							continue
						}
						go func(fPath, rPath string) {
							_ = runNodeServe(itemCtx, tl, fPath, rPath, 4, 2*1024*1024, 0)
						}(item.FullPath, item.RelPath)

						activeSession.Send(session.Message{
							Type:       "batch_item",
							FileName:   item.RelPath,
							FileSize:   item.Size,
							FileID:     item.FileID,
							DataPort:   nodePort,
							TransferID: tid.String(),
							ItemIndex:  idx,
							ChunkSize:  2 * 1024 * 1024,
						})

						select {
						case <-ctx.Done():
							itemCancel()
							return
						case success := <-itemDoneChan:
							itemCancel()
							if !success {
								fmt.Printf("\n[-] Peer failed to download '%s'\n", item.RelPath)
							}
						case <-time.After(60 * time.Second):
							itemCancel()
							fmt.Printf("\n[-] Timeout on '%s' (no response after 60s, skipping)\n", item.RelPath)
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

				go func(ctx context.Context, ip string, port int, fileID, transferID string, itemIdx int, meta protocol.FileMetadata) {
					err := runNodeRecv(ctx, ip, port, ".", activeSession, fileID, transferID, meta)
					if err == nil {
						activeSession.Send(session.Message{Type: "item_complete", ItemIndex: itemIdx})
					} else {
						fmt.Printf("\n[-] Failed to receive '%s': %v\n", meta.FileName, err)
						activeSession.Send(session.Message{
							Type:      "item_failed",
							ItemIndex: itemIdx,
							FileName:  meta.FileName,
							Error:     err.Error(),
						})
					}
				}(itemRecvCtx, activeSession.RemoteIP(), msg.DataPort, msg.FileID, msg.TransferID, msg.ItemIndex, protocol.FileMetadata{
					FileName:  msg.FileName,
					FileSize:  msg.FileSize,
					FileID:    msg.FileID,
					ChunkSize: msg.ChunkSize,
				})

			case "item_complete":
				select {
				case itemDoneChan <- true:
				default:
				}

			case "item_failed":
				select {
				case itemDoneChan <- false:
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

				offered := lastOfferedListener
				lastOfferedListener = nil
				go func(offeredFile, offeredRelPath string, tl *transfer.Listener, resumeOffset int64) {
					if tl != nil {
						_ = runNodeServe(ctx, tl, offeredFile, offeredRelPath, 4, 2*1024*1024, resumeOffset)
					}
					if lastOfferedManifest == nil {
						printPrompt()
					}
				}(lastOfferedFile, lastOfferedRelPath, offered, msg.ResumeBytes)

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

			case session.TypeError: // never answered: two peers must not ping-pong errors
				fmt.Printf("\n[-] The peer could not handle a message: %s (%s)\n", msg.Error, msg.Code)
				printPrompt()

			default: // bible §8.2: an unknown type gets error{code: unsupported}
				if activeSession != nil {
					_ = activeSession.Send(session.Unsupported(msg.Type))
				}
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

// registerNodeTransfer makes a new transfer joinable on the node's port by the session's
// peer, and returns its id and the listener of its data connections.
func registerNodeTransfer(hub *transfer.Hub, s *session.Channel) (transfer.ID, *transfer.Listener, error) {
	peer := sessionPeer(s)
	if peer.IsZero() {
		return transfer.ID{}, nil, errors.New("no paired device")
	}
	id, err := transfer.NewID()
	if err != nil {
		return transfer.ID{}, nil, err
	}
	l, err := hub.Register(id, peer)
	return id, l, err
}

// runNodeServe serves filePath on the data connections of a registered transfer.
func runNodeServe(parentCtx context.Context, tl *transfer.Listener, filePath, relPath string, workers int, chunkSize uint32, resumeOffset int64) error {
	ctx, cancel := context.WithCancel(parentCtx)
	defer cancel()
	sender := engine.NewSender(workers, chunkSize)
	listener := &cliListener{cancel: cancel, isSender: true}
	return sender.ServeOn(ctx, tl, filePath, relPath, listener, resumeOffset)
}

// runNodeRecv downloads a transfer from the paired device's port: every data connection is
// pinned to its key and joins the transfer by its id (CHAN-06).
func runNodeRecv(parentCtx context.Context, ip string, port int, outDir string, s *session.Channel, fileID, transferID string, meta ...protocol.FileMetadata) error {
	tid, err := transfer.ParseID(transferID)
	if err != nil {
		return err
	}
	ctx, cancel := context.WithCancel(parentCtx)
	defer cancel()
	targetAddr := net.JoinHostPort(ip, strconv.Itoa(int(port)))
	receiver := engine.NewReceiver(outDir, 4)
	receiver.SetSecure(transfer.JoinFunc(securechan.Config{Identity: localID, Expected: sessionPeer(s)}, tid))
	listener := &cliListener{cancel: cancel, isSender: false}
	if len(meta) > 0 && meta[0].ChunkSize > 0 {
		return receiver.PullWithMetadata(ctx, targetAddr, listener, meta[0], fileID)
	}
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
	var interactive bool
	sendCmd.BoolVar(&interactive, "i", false, "Require confirmation before sending")
	sendCmd.BoolVar(&interactive, "interactive", false, "Require confirmation before sending")

	_ = sendCmd.Parse(normalizedArgs)
	if sendCmd.NArg() < 1 {
		fmt.Println("Error: Missing file or folder path. Usage: xfer send [-i] <path> [additional_paths...]")
		os.Exit(1)
	}
	if *chunkSizeMB < 1 || *chunkSizeMB > 8 {
		fmt.Println("Error: -chunk must be between 1 and 8 (MB).")
		os.Exit(1)
	}
	chunkSize := uint32(*chunkSizeMB * 1024 * 1024)

	id, trust, err := loadIdentity(configDir())
	if err != nil {
		fmt.Printf("[-] %v\n", err)
		return
	}
	localID = id

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
		runOneShotBatchSend(m, *portFlag, *workersFlag, chunkSize, *createNetwork, interactive, trust)
	} else {
		runOneShotSingleSend(rawPaths[0], *portFlag, *workersFlag, chunkSize, *createNetwork, interactive, trust)
	}
}

func runOneShotBatchSend(m *manifest.Manifest, port int, workers int, chunkSize uint32, createHotspot bool, interactive bool, trust *identity.TrustStore) {
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

	// The device's single port: the receiver pairs on it, then joins the transfer (CHAN-01).
	hub, hubLn, err := startOneShotHub("0.0.0.0", port, trust)
	if err != nil {
		fmt.Printf("[-] Cannot listen on port %d: %v\n", port, err)
		return
	}
	defer hubLn.Close()
	base, err := transfer.NewID()
	if err != nil {
		fmt.Printf("[-] %v\n", err)
		return
	}
	send := newOneShotSend(hub, base, len(m.Items))

	offer := &discovery.TransferOffer{
		FileName: m.SummaryString(),
		FileSize: m.TotalBytes,
		FileID:   m.BatchID,
		IsBatch:  true,
		Batch:    m,
	}
	pCode, _, _ := discovery.GeneratePairingCode(getLocalIP())
	discServer := discovery.NewDiscoveryServer("sender", port, offer)
	discServer.SetPairingCode(pCode)
	discServer.Start(ctx)

	fmt.Println("==================================================")
	fmt.Println("             medXfer Secure Sender                ")
	fmt.Println("==================================================")
	fmt.Printf(" Batch        : %s\n", m.SummaryString())
	fmt.Printf(" Fingerprint  : %s\n", localID.Fingerprint.Short())
	fmt.Printf(" Direct IP    : %s:%d\n", getLocalIP(), port)
	fmt.Printf(" Pairing Code : %s   (xfer recv %s: both screens then confirm)\n", pCode, pCode)
	fmt.Println("--------------------------------------------------")
	startOneShotPairing(ctx, send, port, offerMessage(*offer, port, base), trust)
	fmt.Println("[*] Waiting for receiver to connect...")

	var batchMu sync.Mutex
	var batchPrompted bool
	var batchAllowed bool = true

	for idx, item := range m.Items {
		if ctx.Err() != nil {
			return
		}
		tl := send.listener(ctx, idx) // registered for the paired receiver only (CHAN-06)
		if tl == nil {
			return
		}
		fmt.Printf("\n[%d/%d] Serving '%s' (%.2f MB)...\n", idx+1, m.TotalFiles, item.RelPath, float64(item.Size)/(1024*1024))

		itemCtx, itemCancel := context.WithCancel(ctx)
		sender := engine.NewSender(workers, chunkSize)
		if interactive {
			sender.SetAuthorizer(func(remoteAddr net.Addr, fileName string, fileSize int64) bool {
				batchMu.Lock()
				defer batchMu.Unlock()
				if batchPrompted {
					return batchAllowed
				}
				batchPrompted = true
				host, _, _ := net.SplitHostPort(remoteAddr.String())
				fmt.Printf("\n[?] Receiver at %s connected.\n    Authorize sending folder '%s' (%d files, %.2f MB)? [Y/n]: ", host, m.SummaryString(), m.TotalFiles, float64(m.TotalBytes)/(1024*1024))
				reader := bufio.NewReader(os.Stdin)
				ans, _ := reader.ReadString('\n')
				ans = strings.TrimSpace(strings.ToLower(ans))
				batchAllowed = (ans == "" || ans == "y" || ans == "yes" || ans == "o" || ans == "oui")
				if !batchAllowed {
					fmt.Println("[-] Transfer aborted: declined by sender.")
				}
				return batchAllowed
			})
		}
		listener := &cliListener{cancel: itemCancel, isSender: true}
		err := sender.ServeOn(itemCtx, tl, item.FullPath, item.RelPath, listener, 0)
		itemCancel()
		if err != nil && ctx.Err() == nil {
			fmt.Printf("\n[-] Transfer error on '%s': %v\n", item.RelPath, err)
			return
		}
	}
	fmt.Printf("\n[+] Folder transfer complete! All %d files sent successfully.\n", m.TotalFiles)
}

func runOneShotSingleSend(filePath string, port int, workers int, chunkSize uint32, createHotspot bool, interactive bool, trust *identity.TrustStore) {
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

	// The device's single port: the receiver pairs on it, then joins the transfer (CHAN-01).
	hub, hubLn, err := startOneShotHub("0.0.0.0", port, trust)
	if err != nil {
		fmt.Printf("[-] Cannot listen on port %d: %v\n", port, err)
		return
	}
	defer hubLn.Close()
	base, err := transfer.NewID()
	if err != nil {
		fmt.Printf("[-] %v\n", err)
		return
	}
	send := newOneShotSend(hub, base, 0)

	offer := &discovery.TransferOffer{
		FileName: filepath.Base(filePath), FileSize: info.Size(),
		FileID: engine.GenerateFileID(filePath),
	}
	pCode, _, _ := discovery.GeneratePairingCode(getLocalIP())
	discServer := discovery.NewDiscoveryServer("sender", port, offer)
	discServer.SetPairingCode(pCode)
	discServer.Start(ctx)

	fmt.Println("==================================================")
	fmt.Println("             medXfer Secure Sender                ")
	fmt.Println("==================================================")
	fmt.Printf(" File         : %s (%.2f MB)\n", offer.FileName, float64(offer.FileSize)/(1024*1024))
	fmt.Printf(" Fingerprint  : %s\n", localID.Fingerprint.Short())
	fmt.Printf(" Direct IP    : %s:%d\n", getLocalIP(), port)
	fmt.Printf(" Pairing Code : %s   (xfer recv %s: both screens then confirm)\n", pCode, pCode)
	fmt.Println("--------------------------------------------------")
	startOneShotPairing(ctx, send, port, offerMessage(*offer, port, base), trust)
	fmt.Println("[*] Waiting for receiver to connect...")
	tl := send.listener(ctx, 0) // registered for the paired receiver only (CHAN-06)
	if tl == nil {
		return
	}

	sender := engine.NewSender(workers, chunkSize)
	if interactive {
		sender.SetAuthorizer(func(remoteAddr net.Addr, fileName string, fileSize int64) bool {
			host, _, _ := net.SplitHostPort(remoteAddr.String())
			fmt.Printf("\n[?] Receiver at %s connected.\n    Authorize sending '%s' (%.2f MB)? [Y/n]: ", host, fileName, float64(fileSize)/(1024*1024))
			reader := bufio.NewReader(os.Stdin)
			ans, _ := reader.ReadString('\n')
			ans = strings.TrimSpace(strings.ToLower(ans))
			allowed := (ans == "" || ans == "y" || ans == "yes" || ans == "o" || ans == "oui")
			if !allowed {
				fmt.Println("[-] Transfer aborted: declined by sender.")
			}
			return allowed
		})
	}
	listener := &cliListener{cancel: cancel, isSender: true}
	err = sender.ServeOn(ctx, tl, filePath, "", listener, 0)
	if err != nil && ctx.Err() == nil {
		fmt.Printf("\n[-] Transfer error: %v\n", err)
	}
}

func handleRecv(args []string) {
	normalizedArgs := reorderArgs(args)
	recvCmd := flag.NewFlagSet("recv", flag.ExitOnError)
	ipFlag := recvCmd.String("ip", "", "Direct sender address")
	codeFlag := recvCmd.String("code", "", "6-digit pairing code (e.g. 229-481)")
	outDirFlag := recvCmd.String("out", ".", "Directory to save files")
	workersFlag := recvCmd.Int("workers", 4, "Number of parallel TCP streams")

	_ = recvCmd.Parse(normalizedArgs)

	// The sender's QR code: its key comes from it, with the secret that lets this device in.
	if recvCmd.NArg() > 0 && strings.HasPrefix(recvCmd.Arg(0), "medxfer://") {
		recvFromURI(recvCmd.Arg(0), *outDirFlag, *workersFlag)
		return
	}

	targetAddr := *ipFlag
	if targetAddr != "" && !strings.Contains(targetAddr, ":") {
		targetAddr = net.JoinHostPort(targetAddr, strconv.Itoa(int(defaultPort)))
	}

	pairingCode := ""
	if *codeFlag != "" {
		norm, err := discovery.NormalizePairingCode(*codeFlag)
		if err != nil {
			fmt.Printf("[-] Invalid pairing code '%s': %v\n", *codeFlag, err)
			return
		}
		pairingCode = norm
	} else if recvCmd.NArg() > 0 {
		candidate := recvCmd.Arg(0)
		norm, err := discovery.NormalizePairingCode(candidate)
		if err != nil {
			fmt.Printf("[-] Invalid pairing code '%s': %v\n", candidate, err)
			return
		}
		pairingCode = norm
	}

	// Discovery only finds the sender. It carries neither what is sent nor the sender's key
	// (DISC-06): both come over the secure channel, below.
	var selectedPeer *discovery.Peer
	if targetAddr == "" && pairingCode != "" {
		fmt.Printf("[*] Searching local network for sender with pairing code '%s'...\n", pairingCode)
		ctxResolve, cancelResolve := context.WithTimeout(context.Background(), 4*time.Second)
		peer, err := discovery.ResolvePairingCode(ctxResolve, pairingCode, 4*time.Second)
		cancelResolve()
		if err != nil {
			fmt.Printf("[-] Failed to find sender: %v\n", err)
			return
		}
		selectedPeer = peer
		fmt.Printf("[+] Located sender: %s (%s)\n", peer.DeviceName, peer.HostIP)
	} else if targetAddr == "" {
		selectedPeer = selectOneShotSender()
	} else {
		host, _, _ := net.SplitHostPort(targetAddr)
		selectedPeer = fetchPeer(host)
	}
	if selectedPeer == nil {
		return
	}
	if selectedPeer.Offer == nil || selectedPeer.Port == 0 {
		fmt.Println("[-] No transfer that authenticates its receiver is waiting on this device.\n" +
			"    If it is sending, update medXfer on it.")
		return
	}

	id, trust, err := loadIdentity(configDir())
	if err != nil {
		fmt.Printf("[-] %v\n", err)
		return
	}
	localID = id
	in := bufio.NewReader(os.Stdin)
	senderHost := selectedPeer.HostIP

	// The sender's key comes from the TLS handshake. This device's user accepts it (unless it
	// is a trusted device's), then the sender's user decides about this device (decision D3).
	answer, keyNote := trustOnce, "checked on both screens"
	decide := func(fp identity.Fingerprint) bool {
		if known, changed := keyChanged(trust, senderHost, fp); changed {
			fmt.Println(keyChangedAlert(known, senderHost, fp))
			return false
		}
		if p, ok := trustedPeer(trust, fp); ok {
			keyNote = fmt.Sprintf("trusted device '%s'", p.Alias)
		} else {
			fmt.Print(trustQuestion(selectedPeer.DeviceName, fp, id.Fingerprint))
			ans, _ := in.ReadString('\n')
			if answer = parseTrustAnswer(ans); answer == trustRefused {
				fmt.Println("[-] Download cancelled: sender not trusted.")
				return false
			}
		}
		fmt.Println("[*] Waiting for the sender to accept this device...")
		return true
	}
	pairAddr := net.JoinHostPort(senderHost, strconv.Itoa(selectedPeer.Port)) // the sender's single port
	fmt.Printf("[*] Connecting to %s...\n", pairAddr)
	name, _ := os.Hostname()
	o := pairing.Options{Identity: id, Trust: trust, Once: true, DeviceName: name}
	r, err := pairing.Ask(context.Background(), pairAddr, decide, trustPromptTimeout+10*time.Second, o)
	if errors.Is(err, pairing.ErrDeclined) {
		return // decide said why
	}
	if err != nil {
		fmt.Printf("[-] The sender did not accept this device: %v\n", err)
		return
	}
	if err := rememberPeer(trust, r.Fingerprint, r.Name, senderHost, answer); err != nil {
		fmt.Printf("[-] Could not save the trusted device: %v\n", err)
	}
	receiveAccepted(r, senderHost, keyNote, in, *outDirFlag, *workersFlag)
}

// recvFromURI receives a one-shot send from its QR code: the sender's key comes from it, so
// there is no trust question, and nothing is saved (PAIR-08).
func recvFromURI(raw, outDir string, workers int) {
	u, err := pairing.Parse(raw, time.Now())
	if errors.Is(err, pairing.ErrExpired) {
		fmt.Println("[-] This QR code has expired: use the one the sender shows now.")
		return
	}
	if err != nil {
		fmt.Printf("[-] %v\n", err)
		return
	}
	id, _, err := loadIdentity(configDir())
	if err != nil {
		fmt.Printf("[-] %v\n", err)
		return
	}
	localID = id
	name, _ := os.Hostname()
	fmt.Printf("[*] Connecting to %s (key %s)...\n", u.Addr(), u.Fingerprint.Short())
	r, err := pairing.Join(context.Background(), u, pairing.Options{Identity: id, Once: true, DeviceName: name})
	if err != nil {
		fmt.Printf("[-] The sender refused this device: %v\n", err)
		return
	}
	receiveAccepted(r, u.Host, "from the QR code", bufio.NewReader(os.Stdin), outDir, workers)
}

// receiveAccepted shows the offer that came with pair_accept, inside the secure channel, and
// downloads it if the user confirms. Every data connection is pinned to the key the pairing
// verified (WP-3.4).
func receiveAccepted(r pairing.Result, host, keyNote string, in *bufio.Reader, outDir string, workers int) {
	m := r.Offer
	base, err := transfer.ParseID(m.TransferID)
	if m.DataPort == 0 || err != nil {
		fmt.Println("[-] This device offers no transfer (to pair, run: xfer pair <uri>).")
		return
	}
	offer := &discovery.TransferOffer{FileName: m.FileName, FileSize: m.FileSize, FileID: m.FileID, IsBatch: m.Batch != nil, Batch: m.Batch}
	targetAddr := net.JoinHostPort(host, strconv.Itoa(m.DataPort))
	cfg := securechan.Config{Identity: localID, Expected: r.Fingerprint}

	printIncoming(offer, r.Name, targetAddr)
	fmt.Printf(" Key    : %s (%s)\n", r.Fingerprint.Short(), keyNote)
	fmt.Println("--------------------------------------------------")
	fmt.Print("[?] Confirm download? [Y/n]: ")
	ans, _ := in.ReadString('\n')
	ans = strings.TrimSpace(strings.ToLower(ans))
	if ans != "" && ans != "y" && ans != "yes" && ans != "o" && ans != "oui" {
		fmt.Println("[-] Download cancelled by user.")
		return
	}
	pullOffer(targetAddr, outDir, workers, offer, cfg, base)
}

// printIncoming shows what the sender offers.
func printIncoming(o *discovery.TransferOffer, from, addr string) {
	fmt.Println("\n==================================================")
	fmt.Println("             medXfer Incoming Transfer            ")
	fmt.Println("==================================================")
	if o.IsBatch {
		fileCount := 0
		if o.Batch != nil {
			fileCount = o.Batch.TotalFiles
		}
		fmt.Printf(" Folder : %s\n", o.FileName)
		if fileCount > 0 {
			fmt.Printf(" Files  : %d\n", fileCount)
		}
		fmt.Printf(" Size   : %.2f MB\n", float64(o.FileSize)/(1024*1024))
	} else {
		fmt.Printf(" File   : %s (%.2f MB)\n", o.FileName, float64(o.FileSize)/(1024*1024))
	}
	fmt.Printf(" From   : %s (%s)\n", from, addr)
}

// pullOffer downloads the offer from targetAddr, the sender's single port. Every data
// connection is pinned to the sender's key (cfg) and joins its transfer: base for a file,
// base.Item(i) for item i of a folder (CHAN-06).
func pullOffer(targetAddr, outDir string, workers int, o *discovery.TransferOffer, cfg securechan.Config, base transfer.ID) {
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

	if o.IsBatch && o.Batch != nil {
		runOneShotBatchRecv(ctx, targetAddr, outDir, workers, o.Batch, cfg, base)
		return
	}
	fmt.Printf("\n[*] Connecting to sender at %s...\n", targetAddr)
	receiver := engine.NewReceiver(outDir, workers)
	receiver.SetSecure(transfer.JoinFunc(cfg, base))
	listener := &cliListener{cancel: cancel, isSender: false}
	err := receiver.Pull(ctx, targetAddr, listener, o.FileID)
	if err != nil && ctx.Err() == nil {
		fmt.Printf("\n[-] Download failed: %v\n", err)
	}
}

func runOneShotBatchRecv(ctx context.Context, targetAddr, outDir string, workers int, m *manifest.Manifest, cfg securechan.Config, base transfer.ID) {
	fmt.Printf("\n[+] Downloading folder %s from %s...\n", m.SummaryString(), targetAddr)

	for idx, item := range m.Items {
		if ctx.Err() != nil {
			return
		}
		fmt.Printf("\n[%d/%d] Receiving '%s' (%.2f MB)...\n", idx+1, m.TotalFiles, item.RelPath, float64(item.Size)/(1024*1024))

		itemCtx, itemCancel := context.WithCancel(ctx)
		receiver := engine.NewReceiver(outDir, workers)
		receiver.SetSecure(transfer.JoinFunc(cfg, base.Item(idx)))
		listener := &cliListener{cancel: itemCancel, isSender: false}
		err := receiver.Pull(itemCtx, targetAddr, listener, item.FileID)
		itemCancel()
		if err != nil && ctx.Err() == nil {
			fmt.Printf("\n[-] Download failed on '%s': %v\n", item.RelPath, err)
			return
		}
	}
	fmt.Printf("\n[+] Folder download complete! All %d files saved successfully.\n", m.TotalFiles)
}

// fetchPeer asks the device at hostIP whether it is sending. Its answer only says that a
// transfer is waiting and where to pair for it (DISC-06).
func fetchPeer(hostIP string) *discovery.Peer {
	conn, err := net.DialTimeout("tcp4", net.JoinHostPort(hostIP, strconv.Itoa(int(discovery.DiscoveryPort))), 800*time.Millisecond)
	if err != nil {
		fmt.Printf("[-] No medXfer device answers at %s: %v\n", hostIP, err)
		return nil
	}
	defer conn.Close()
	_ = conn.SetReadDeadline(time.Now().Add(1500 * time.Millisecond))
	data, err := io.ReadAll(conn)
	var p discovery.Peer
	if err != nil || json.Unmarshal(data, &p) != nil {
		fmt.Printf("[-] No medXfer device answers at %s.\n", hostIP)
		return nil
	}
	p.HostIP = hostIP
	return &p
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
			// What is sent is shown only once both devices have accepted each other.
			for i, s := range senders {
				kind := "file"
				if s.Offer.IsBatch {
					kind = "folder"
				}
				fmt.Printf("  [%d] %s (%s) - %s waiting\n", i+1, s.DeviceName, s.HostIP, kind)
			}
		}
		fmt.Print("Select an option or enter pairing code [r to refresh, q to quit]: ")
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
		if normCode, err := discovery.NormalizePairingCode(input); err == nil {
			fmt.Printf("[*] Searching for sender with pairing code '%s'...\n", normCode)
			ctxResolve, cancelResolve := context.WithTimeout(context.Background(), 3*time.Second)
			peer, err := discovery.ResolvePairingCode(ctxResolve, normCode, 3*time.Second)
			cancelResolve()
			if err == nil {
				return peer
			}
			fmt.Printf("[-] Could not find device with pairing code '%s'\n", normCode)
		}
	}
}

func handleDaemon(args []string) {
	normalizedArgs := reorderArgs(args)
	daemonCmd := flag.NewFlagSet("daemon", flag.ExitOnError)
	portFlag := daemonCmd.Int("port", 19999, "Port for Headless API (HTTP & WebSocket)")
	outDirFlag := daemonCmd.String("out", "", "Default directory to save incoming files")
	nameFlag := daemonCmd.String("name", "", "Custom device name for discovery")
	webShareFlag := daemonCmd.Bool("web-share", false, "Open the Web Share guest portal to the LAN at startup")

	_ = daemonCmd.Parse(normalizedArgs)
	defer startJournal()()

	srv := api.NewDaemonServer(*portFlag, *outDirFlag, *nameFlag)
	// Web Share is an optional module, disabled by default (bible ch. 11, rule R3): until it is
	// enabled (--web-share, or later from the dashboard), no LAN device can try the PIN on /share.
	srv.SetWebShareEnabled(*webShareFlag)

	ln, err := srv.Listen(*portFlag)
	if err != nil {
		fmt.Printf("[-] Failed to start Daemon listener: %v\n", err)
		os.Exit(1)
	}

	// Scripts read the control token from this file (decision D0-2). Print its path, never its value.
	tokenPath, err := writeControlToken(srv.ControlToken())
	if err != nil {
		fmt.Printf("[-] Failed to write control token: %v\n", err)
		os.Exit(1)
	}

	st := srv.GetStatus()
	localIP := st.LocalIP
	if localIP == "" || localIP == "127.0.0.1" {
		localIP = getLocalIP()
	}
	if localIP == "" {
		localIP = "127.0.0.1"
	}
	localURL := fmt.Sprintf("http://localhost:%d", st.LocalPort)
	remoteURL := fmt.Sprintf("http://%s:%d", localIP, st.LocalPort)

	fmt.Println("==================================================")
	fmt.Println("             medXfer Daemon & Server              ")
	fmt.Println("==================================================")
	fmt.Printf(" Device Name  : %s\n", st.DeviceName)
	fmt.Printf(" Local IP     : %s\n", localIP)
	fmt.Printf(" Port         : %d\n", st.LocalPort)
	fmt.Printf(" Pairing Code : %s\n", st.PairingCode)
	fmt.Println("--------------------------------------------------")
	if api.DashboardAvailable {
		fmt.Println(" Web Dashboard (development build, this device only):")
		fmt.Printf("   👉 %s/\n", localURL)
	} else {
		fmt.Println(" Web Dashboard: not in this build (development tool, build with -tags devui)")
	}
	fmt.Printf(" Control token file: %s\n", tokenPath)
	fmt.Println("--------------------------------------------------")
	if *webShareFlag {
		fmt.Printf(" Web Share PIN: %s\n", st.WebSharePIN)
		fmt.Println(" Web Share portal (other devices):")
		fmt.Printf("   👉 %s/share\n", remoteURL)
		fmt.Println(" Scan QR code to connect from phone / PC:")
		discovery.PrintTerminalQR(remoteURL + "/share")
	} else {
		fmt.Println(" Web Share    : disabled (start with --web-share, or enable it from the dashboard)")
	}
	fmt.Println("--------------------------------------------------")
	fmt.Println(" Daemon is running. Press Ctrl+C to stop.")

	// Listen for OS signals to stop cleanly
	sigChan := make(chan os.Signal, 1)
	signal.Notify(sigChan, os.Interrupt, syscall.SIGTERM)
	go func() {
		<-sigChan
		fmt.Println("\n[*] Stopping Daemon...")
		srv.Stop()
		os.Exit(0)
	}()

	if err := srv.Serve(ln); err != nil && err != http.ErrServerClosed {
		fmt.Printf("[-] Server error: %v\n", err)
	}
}

// writeControlToken stores the control token in <config dir>/control.token, readable only by
// the user (0600 on Unix; on Windows the file lives in the user's profile).
func writeControlToken(token string) (string, error) {
	dir := filepath.Dir(api.GetConfigFilePath())
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return "", err
	}
	p := filepath.Join(dir, "control.token")
	if err := os.WriteFile(p, []byte(token+"\n"), 0o600); err != nil {
		return "", err
	}
	_ = os.Chmod(p, 0o600) // WriteFile keeps the mode of an existing file
	return p, nil
}

func handleShare(args []string) {
	normalizedArgs := reorderArgs(args)
	shareCmd := flag.NewFlagSet("share", flag.ExitOnError)
	portFlag := shareCmd.Int("port", 19999, "Port for Web Share HTTP portal")
	pinFlag := shareCmd.String("pin", "", "Custom 4-digit security PIN (default: random)")
	autoAcceptFlag := shareCmd.Bool("auto-accept", false, "Auto-accept incoming uploads without terminal prompt")
	outDirFlag := shareCmd.String("out", "", "Directory to save incoming uploaded files")
	nameFlag := shareCmd.String("name", "", "Custom device name")

	_ = shareCmd.Parse(normalizedArgs)
	defer startJournal()()
	rawPaths := shareCmd.Args()

	srv := api.NewDaemonServer(*portFlag, *outDirFlag, *nameFlag)
	srv.DisableControlSurface() // portal only: no dashboard, /ws or /status in share mode
	srv.SetWebShareEnabled(true)
	if *pinFlag != "" {
		srv.SetPIN(*pinFlag)
	}
	if *autoAcceptFlag {
		srv.SetAutoAccept(true)
	}

	if len(rawPaths) > 0 {
		if err := srv.SharePaths(rawPaths); err != nil {
			fmt.Printf("[-] Failed to prepare files for sharing: %v\n", err)
			os.Exit(1)
		}
	}

	ln, err := srv.Listen(*portFlag)
	if err != nil {
		fmt.Printf("[-] Failed to start Web Share listener: %v\n", err)
		os.Exit(1)
	}

	st := srv.GetStatus()
	portalURL := st.PortalURL
	if portalURL == "" {
		portalURL = fmt.Sprintf("http://%s:%d/share?pin=%s", discovery.GetPrimaryLocalIP(), st.LocalPort, st.WebSharePIN)
	}

	fmt.Println("==================================================")
	fmt.Println("             medXfer Instant Web Share            ")
	fmt.Println("==================================================")
	if len(rawPaths) > 0 {
		fmt.Printf(" [SHARING] %d file/folder path(s):\n", len(rawPaths))
		for _, p := range rawPaths {
			fmt.Printf("   • %s\n", p)
		}
	} else {
		fmt.Println(" [SHARING] Portal ready for incoming uploads and downloads.")
	}
	fmt.Println("--------------------------------------------------")
	fmt.Printf(" Access URL : %s\n", portalURL)
	fmt.Printf(" Security PIN: %s\n", st.WebSharePIN)
	fmt.Println("--------------------------------------------------")
	fmt.Println(" Scan QR code with your phone / device camera:")
	discovery.PrintTerminalQR(portalURL)
	fmt.Println("--------------------------------------------------")
	fmt.Println(" Web Share is running. Press Ctrl+C to stop.")

	// Listen for OS signals to stop cleanly
	sigChan := make(chan os.Signal, 1)
	signal.Notify(sigChan, os.Interrupt, syscall.SIGTERM)
	go func() {
		<-sigChan
		fmt.Println("\n[*] Stopping Web Share...")
		srv.Stop()
		os.Exit(0)
	}()

	if err := srv.Serve(ln); err != nil && err != http.ErrServerClosed {
		fmt.Printf("[-] Server error: %v\n", err)
	}
}

func printUsage() {
	fmt.Printf("==================================================\n" +
		"               medXfer - Fast P2P Transfer        \n" +
		"==================================================\n" +
		"Usage:\n" +
		"  xfer daemon [--port 19999] [--web-share]       (Headless daemon & control WebSocket)\n" +
		"  xfer dev-ui [--port 19999] [--web-share]       (Daemon with the web dashboard; devui builds)\n" +
		"  xfer node                                      (Persistent interactive CLI: QR code or fingerprints)\n" +
		"  xfer share <file_or_folder...>                 (Instant Web Share with QR code & PIN)\n" +
		"  xfer send [-i] <file_or_folder> [more...]      (Direct transfer: shows a QR code; -i prompts)\n" +
		"  xfer recv 'medxfer://...'                      (Receive from the sender's QR code)\n" +
		"  xfer recv [pairing_code]                       (Via 6-digit code or discovery; both confirm)\n" +
		"  xfer recv --ip <addr>                          (Direct connect by IP)\n" +
		"  xfer report show|compare|list [--json]         (Transfer diagnostic reports)\n" +
		"  xfer ctl [--script scenario.jsonl]             (Drive an embedded daemon with JSON lines)\n" +
		"  xfer bench disk|net [...]                      (Disk and network benchmarks)\n" +
		"  xfer bundle [-o FILE.zip]                      (Diagnostic bundle for a bug report)\n" +
		"  xfer id                                        (This device's fingerprint)\n" +
		"  xfer trust list|forget <fingerprint>           (Trusted devices)\n" +
		"  xfer pair --show | xfer pair <medxfer://...>   (Pair two devices by QR code)\n" +
		"\n" +
		"Examples on Android / Termux:\n" +
		"  ./xfer-android-arm64 daemon                    -> Starts Web UI on http://localhost:19999\n" +
		"  ./xfer-android-arm64 node                      -> Interactive CLI (scan, pair, send, recv)\n" +
		"  ./xfer-android-arm64 share /sdcard/video.mp4   -> Instant Web Share with QR Code\n" +
		"==================================================\n")
}
