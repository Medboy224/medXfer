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
	"sync/atomic"
	"syscall"
	"time"

	"github.com/Medboy224/medXfer/pkg/api"
	"github.com/Medboy224/medXfer/pkg/diag"
	"github.com/Medboy224/medXfer/pkg/discovery"
	"github.com/Medboy224/medXfer/pkg/engine"
	"github.com/Medboy224/medXfer/pkg/hotspot"
	"github.com/Medboy224/medXfer/pkg/identity"
	"github.com/Medboy224/medXfer/pkg/pairing"
	"github.com/Medboy224/medXfer/pkg/protocol"
	"github.com/Medboy224/medXfer/pkg/securechan"
	"github.com/Medboy224/medXfer/pkg/session"
	"github.com/Medboy224/medXfer/pkg/transfer"
)

// defaultPort is the device's single port: pairing, session and data (CHAN-01). Every mode
// uses the same one, so that one firewall rule covers medXfer (#133).
const defaultPort = 18887

// listenNodePort opens the node's single port: defaultPort, or a free one when another medXfer
// on this device holds it (CHAN-01), said on the screen. Silently left without a port, the node
// used to pair (it dials out) but every download from it reached the other program.
// bind is 0.0.0.0 for the network; tests use the loopback (no firewall prompt on Windows).
func listenNodePort(bind string) (net.Listener, error) {
	l, err := net.Listen("tcp4", net.JoinHostPort(bind, strconv.Itoa(defaultPort)))
	if err == nil {
		return l, nil
	}
	if l, err = net.Listen("tcp4", net.JoinHostPort(bind, "0")); err != nil {
		return nil, err
	}
	fmt.Printf("[!] Port %d is taken (another medXfer on this device: xfer dev-ui, daemon or node?): using port %d.\n",
		defaultPort, l.Addr().(*net.TCPAddr).Port)
	fmt.Println("    On Windows, the firewall must let this port in, or the other device cannot download from here.")
	return l, nil
}

// isLocalIP reports whether ipStr is this device; also under Android, which refuses
// net.InterfaceAddrs (#132).
func isLocalIP(ipStr string) bool {
	return ipStr == "localhost" || discovery.IsLocalNetworkIP(ipStr)
}

func getLocalIP() string {
	return discovery.GetPrimaryLocalIP()
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
	case "daemon", "d", "server", "srv", "dev-ui", "devui":
		// The daemon and its control WebSocket serve the web dashboard only: a development tool
		// (WP-6.6, decision D7 of the J6a plan). The app embeds the core (cmd/libmedxfer) and
		// the commands below call it in-process.
		if !api.DashboardAvailable {
			fmt.Fprintln(os.Stderr, "[-] xfer "+cmd+": the daemon serves the web dashboard, a development tool not part of this build.\n"+
				"    Build with: go build -tags devui -o xfer ./cmd/xfer   (build.bat does)\n"+
				"    To send or receive, use xfer send, xfer recv, xfer pair or xfer node.")
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
	case "name":
		handleName(args[1:])
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

// reorderArgs puts the flags before the positional arguments, so that `xfer recv FILE --out D`
// parses like `xfer recv --out D FILE`. A flag takes the next argument as its value unless fs
// declares it boolean (or it is written -flag=value).
func reorderArgs(fs *flag.FlagSet, args []string) []string {
	var flags, positionals, rest []string
	for i := 0; i < len(args); i++ {
		arg := args[i]
		if arg == "--" { // what follows is positional, even if it starts with "-"
			rest = append([]string{"--"}, args[i+1:]...)
			break
		}
		if !strings.HasPrefix(arg, "-") || arg == "-" {
			positionals = append(positionals, arg)
			continue
		}
		flags = append(flags, arg)
		name := strings.TrimLeft(arg, "-")
		if strings.Contains(name, "=") {
			continue
		}
		f := fs.Lookup(name)
		if f == nil {
			continue
		}
		if b, ok := f.Value.(interface{ IsBoolFlag() bool }); ok && b.IsBoolFlag() {
			continue
		}
		if i+1 < len(args) {
			i++
			flags = append(flags, args[i])
		}
	}
	if rest != nil {
		return append(append(flags, "--"), append(positionals, rest[1:]...)...)
	}
	return append(flags, positionals...)
}

func handleNode() {
	nodeLn, err := listenNodePort("0.0.0.0")
	if err != nil {
		fmt.Fprintf(os.Stderr, "[-] Cannot listen: %v\n", err)
		return
	}
	defer nodeLn.Close()
	nodePort := nodeLn.Addr().(*net.TCPAddr).Port // announced by discovery, the QR code, the PIN and offers

	nodePairingCode, _, _ := discovery.GeneratePairingCode(getLocalIP())

	ctxDisc, cancelDisc := context.WithCancel(context.Background())
	defer cancelDisc()
	discOffer := &discovery.TransferOffer{FileName: "medXfer-Node", FileSize: 0}
	discServer := discovery.NewDiscoveryServer("node", nodePort, discOffer)
	discServer.SetPairingCode(nodePairingCode)
	discServer.Start(ctxDisc)

	nodeID, nodeTrust, err := loadIdentity(configDir())
	if err != nil {
		fmt.Fprintf(os.Stderr, "[-] %v\n", err)
		return
	}
	discServer.SetTags(announceTagsOf(nodeTrust, nodeID.Fingerprint)) // devices of mutual trust recognize this one (DISC-11)
	// Control connections: unknown keys start UNAUTH. The pairing code only finds this node on
	// the network: a device pairs by a PIN or a QR code, or as a device of mutual trust (bible
	// chapter 6, PAIR-09, PAIR-12).
	pairCfg := securechan.Config{Identity: nodeID, Trust: nodeTrust, AllowUnknown: true, Gate: securechan.NewGate()}
	localID = nodeID
	var invite nodeInvitation               // the QR code shown by `pair --show`
	var nodePIN atomic.Pointer[pairing.PIN] // the PIN shown by `pair --pin` (bible §6.2)
	var offerTrustOnPair atomic.Bool        // pair --pin --trust: offer mutual trust once paired
	// The node's single port: control connections to the loop below, data connections to the
	// transfers registered with their peer's key (CHAN-01, CHAN-06).
	nodeHub := transfer.NewHub(pairCfg)
	nodeHub.Wrap = engine.WrapAccepted

	fmt.Println("==================================================")
	fmt.Println("             medXfer Persistent Node              ")
	fmt.Println("==================================================")
	myName := api.DeviceName() // sent once paired; never announced (DISC-06)
	fmt.Printf(" Device Name : %s   (announced as %s)\n", myName, discServer.GetPeer().DeviceName)
	fmt.Printf(" Local IP    : %s\n", getLocalIP())
	fmt.Printf(" Find code   : %s   (locates this node; trust comes from its key)\n", nodePairingCode)
	fmt.Printf(" Fingerprint : %s\n", nodeID.Fingerprint.Short())
	fmt.Println("--------------------------------------------------")
	fmt.Println(" Commands:")
	fmt.Println("   scan              - Find devices on the network")
	fmt.Println("   pair <n|ip|code>  - Pair: that device shows a PIN, type it here [--trust]")
	fmt.Println("   pair --show       - Show a QR code for the other device [--trust]")
	fmt.Println("   pair <medxfer://> - Pair with the device that shows a QR code [--trust]")
	fmt.Println("   pair --pin        - Show a PIN for the other device [--trust]")
	fmt.Println("   trust             - Offer mutual trust to the paired device: no PIN next time")
	fmt.Println("   send <filepath>   - Send file/folder to paired device")
	fmt.Println("   stop              - Cancel an active transfer gracefully")
	fmt.Println("   disconnect        - End pairing session")
	fmt.Println("   exit              - Shutdown node")
	fmt.Println("--------------------------------------------------")

	var activeSession *session.Channel
	var incomingOffer *session.Message // an offer whose pages are still arriving (D2)
	var pendingOffer *session.Message  // a complete offer waiting for the user's answer
	var lastScannedPeers []discovery.Peer
	var lastOffered *nodeOffer // what this node offered last, until the peer answers
	var transferCancel context.CancelFunc
	var printPrompt func()

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

	// The trust negotiation of the active session (PAIR-10): the other device's offer waits for
	// the next line typed; an offer of ours is over when answered, or after TrustAnswerTime.
	var talk atomic.Pointer[pairing.TrustTalk]
	var talkGen atomic.Int64 // bumped when an offer of ours is answered, or the session ends
	var sessionMutual atomic.Bool
	pendingTrustAsk := false
	endTalk := func() {
		talk.Store(nil)
		talkGen.Add(1)
		sessionMutual.Store(false)
		pendingTrustAsk = false
	}
	offerTrust := func() {
		t := talk.Load()
		if activeSession == nil || t == nil {
			fmt.Println("[-] You must 'pair' with a device first.")
			return
		}
		if sessionMutual.Load() {
			fmt.Println("[*] This device is already a device of mutual trust.")
			return
		}
		if err := activeSession.Send(t.Offer()); err != nil {
			fmt.Printf("[-] %v\n", err)
			return
		}
		g := talkGen.Add(1)
		fmt.Printf("[*] Mutual trust offered: waiting for the other device's answer (%d s)...\n", int(pairing.TrustAnswerTime.Seconds()))
		time.AfterFunc(pairing.TrustAnswerTime, func() {
			if talkGen.Load() == g {
				fmt.Println("\n[-] No answer to the trust offer: the session stays temporary.")
				printPrompt()
			}
		})
	}
	// startSession makes ch the active session with the device fp (pairing done). offer: offer
	// mutual trust at once (--trust).
	startSession := func(ch *session.Channel, fp identity.Fingerprint, name, ip string, mutual, offer, incoming bool) {
		if err := touchPeer(nodeTrust, fp, name, ip); err != nil {
			fmt.Printf("\n[-] Could not save the trusted device: %v\n", err)
		}
		ch.Authenticate()
		activeSession = ch
		talk.Store(pairing.NewTrustTalk(nodeTrust, nodeID.Fingerprint, identity.Peer{Fingerprint: fp, Alias: name, LastAddress: ip}))
		talkGen.Add(1)
		sessionMutual.Store(mutual)
		how := "for this session only, nothing saved"
		if mutual {
			how = "a device of mutual trust"
		}
		if incoming {
			fmt.Printf("\n\n[+] Device '%s' (%s) paired, key %s: %s. Press Enter to refresh prompt.\n", name, ip, fp.Short(), how)
		} else {
			fmt.Printf("[+] Paired with '%s' (%s), key %s: %s.\n", name, ip, fp.Short(), how)
		}
		go func(c *session.Channel) {
			for {
				m, err := c.Read()
				if err != nil {
					disconnectChan <- true
					return
				}
				msgChan <- m
			}
		}(ch)
		if offer && !mutual {
			offerTrust()
		}
	}

	// PINs on demand (PAIR-11): drawn when a device picks this node in its list, unless a PIN
	// shown by `pair --pin` is still valid. The PIN goes to the screen only.
	var demandFrom atomic.Value
	pinDemand := pairing.NewPINOnDemand(pairing.PINConfig{Show: func(code string, expires time.Time) {
		from, _ := demandFrom.Load().(string)
		switch {
		case code != "":
			fmt.Printf("\n\n[?] The device at %s wants to pair: it must type this PIN:  %s   (until %s)\n",
				from, pairing.FormatPIN(code), expires.Format("15:04:05"))
		case expires.IsZero():
			fmt.Println("\n[-] Too many wrong PINs: no more PINs for devices that ask (pair --pin starts again).")
		default:
			fmt.Printf("\n[-] Too many wrong PINs: a new PIN at %s.\n", expires.Format("15:04:05"))
		}
		printPrompt()
	}})

	go func() {
		scanner := bufio.NewScanner(os.Stdin)
		for scanner.Scan() {
			inputChan <- scanner.Text()
		}
	}()

	go func() {
		control := nodeHub.Control()
		go func() { _ = nodeHub.Serve(nodeLn) }()
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

			if msg.Type != "pair_request" && msg.Type != pairing.TypePIN && msg.Type != pairing.TypePINWanted {
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
			// The pairing code, if any, is ignored (WP-3.6b). A PIN (shown, or on demand) or a QR
			// code's secret admits a device for the session; a device of mutual trust needs
			// neither (P3). Anything else is told to use a PIN or a QR code (PAIR-12).
			offer, mutual := false, false
			switch {
			case msg.Type == pairing.TypePIN || msg.Type == pairing.TypePINWanted:
				shown := nodePIN.Load()
				if shown != nil && !shown.Live() {
					shown = nil
				}
				var name string
				var err error
				if msg.Type == pairing.TypePINWanted {
					demandFrom.Store(peerIP)
					name, err = pairing.AnswerPINWanted(ch, conn, func() (*pairing.PIN, error) {
						if shown != nil {
							return shown, nil
						}
						return pinDemand.Get()
					}, nodeID.Fingerprint)
				} else {
					pin := shown
					if pin == nil {
						pin = pinDemand.Current() // asked for on another connection
					}
					name, err = pairing.RespondPIN(ch, conn, msg, pin, nodeID.Fingerprint)
				}
				if errors.Is(err, pairing.ErrNoPINTyped) {
					ch.Close()
					continue
				}
				if err != nil {
					_ = ch.Send(session.Message{Type: pairing.TypeReject, FileName: pairing.ErrRefused.Error()}) // PAIR-07
					ch.Close()
					fmt.Printf("\n[-] PIN pairing attempt from %s refused.\n", peerIP)
					printPrompt()
					continue
				}
				if shown == nil {
					pinDemand.Close() // a PIN serves one pairing (PAIR-02)
				}
				_ = conn.SetDeadline(time.Time{})
				msg.DeviceName = name // sent only after both confirmations (PAIR-07)
				offer = shown != nil && offerTrustOnPair.Load()
			case msg.Secret != "":
				var ok bool
				if offer, ok = invite.redeem(msg.Secret); !ok {
					_ = ch.Send(session.Message{Type: "pair_reject", FileName: pairing.ErrRefused.Error()}) // PAIR-07
					ch.Close()
					fmt.Printf("\n[-] Pairing attempt from %s refused: wrong or expired QR code.\n", peerIP)
					printPrompt()
					continue
				}
			case pairing.Mutual(nodeTrust, peerFP, msg.TrustsYou):
				mutual = true
			default:
				switch {
				case msg.TrustsYou:
					fmt.Printf("\n[*] '%s' (%s) trusts this node, but this node no longer trusts it: it must pair by PIN.\n", msg.DeviceName, peerIP)
				case nodeTrust.Contains(peerFP):
					fmt.Printf("\n[*] This node trusts '%s' (%s), but it no longer trusts this node: it must pair by PIN.\n", msg.DeviceName, peerIP)
				}
				_ = ch.Send(session.Message{Type: "pair_reject", FileName: pairing.ErrPINRequired.Error(), Code: pairing.CodePINRequired})
				ch.Close()
				printPrompt()
				continue
			}
			_ = ch.Send(session.Message{
				Type:       "pair_accept",
				DeviceName: myName,
				TrustsYou:  pairing.Declares(nodeTrust, peerFP),
			})
			devLabel := msg.DeviceName
			if devLabel == "" {
				devLabel = "Remote Node"
			}
			startSession(ch, peerFP, devLabel, peerIP, mutual, offer, true)
		}
	}()

	printPrompt = func() {
		if pendingTrustAsk {
			fmt.Print("\n[?] Accept mutual trust? [y/N]: ")
			return
		}
		if pendingOffer != nil {
			fmt.Printf("\n[?] Accept %s? [Y/n]: ", offerSummary(*pendingOffer.Offer))
		} else if activeSession != nil {
			fmt.Printf("\n[Paired: %s] > ", activeSession.RemoteIP())
		} else {
			fmt.Print("\nmedXfer > ")
		}
	}
	printPrompt()

	// takeNodeOffer adds a page to the offer being received; once it is complete and valid,
	// it asks the user. A bad offer is refused.
	takeNodeOffer := func(page []protocol.OfferItem) *session.Message {
		tid := incomingOffer.Offer.TransferID
		m, err := completeNodeOffer(&incomingOffer, page)
		if err != nil {
			fmt.Printf("\n[-] Refused the peer's offer: %v\n", err)
			if activeSession != nil {
				_ = activeSession.Send(session.Message{Type: "reject", TransferID: tid})
			}
			printPrompt()
		} else if m != nil {
			printPrompt()
		}
		return m
	}

	for {
		select {
		case text := <-inputChan:
			text = strings.TrimSpace(text)
			if resume := takeDiskResume(); resume != nil && !pendingTrustAsk && pendingOffer == nil {
				fmt.Println("[*] Resuming...")
				resume()
				printPrompt()
				continue
			}
			if pendingTrustAsk { // the other device's offer of mutual trust (PAIR-10)
				pendingTrustAsk = false
				a := strings.ToLower(text)
				yes := a == "y" || a == "yes" || a == "o" || a == "oui"
				if t := talk.Load(); t != nil && activeSession != nil {
					if m, err := t.Answer(yes); err != nil {
						fmt.Println("[-] Too late: the trust offer has expired.")
					} else {
						_ = activeSession.Send(m)
						if !yes {
							fmt.Println("[-] Mutual trust declined: the session stays temporary.")
						}
					}
				}
				printPrompt()
				continue
			}
			if pendingOffer != nil {
				m := pendingOffer
				pendingOffer = nil
				if activeSession == nil {
					printPrompt()
					continue
				}
				if strings.ToLower(text) == "n" {
					activeSession.Send(session.Message{Type: "reject", TransferID: m.Offer.TransferID})
					fmt.Println("[-] Transfer rejected.")
					printPrompt()
					continue
				}
				if transferCancel != nil {
					transferCancel()
					transferCancel = nil
				}
				offer := *m.Offer
				tid, err := transfer.ParseID(offer.TransferID)
				if err != nil {
					fmt.Printf("[-] Invalid offer: %v\n", err)
					activeSession.Send(session.Message{Type: "reject", TransferID: offer.TransferID})
					printPrompt()
					continue
				}
				if err := checkSpace(".", offer, sessionPeer(activeSession)); err != nil {
					fmt.Printf("[-] Cannot receive here: %v.\n", err)
					activeSession.Send(session.Message{Type: "reject", TransferID: offer.TransferID, Code: "no_space", Error: err.Error()})
					printPrompt()
					continue
				}
				fmt.Printf("[+] Transfer accepted. Receiving %s...\n", offerSummary(offer))
				activeSession.Send(session.Message{Type: "accept", TransferID: offer.TransferID})

				var ctx context.Context
				ctx, transferCancel = context.WithCancel(context.Background())
				addr := net.JoinHostPort(activeSession.RemoteIP(), strconv.Itoa(m.DataPort))
				fmt.Printf("[*] Connecting to sender at %s...\n", addr) // up to ~45 s when a firewall drops it (#133)
				// Every data connection is pinned to the session's peer and joins its transfer (CHAN-06).
				cfg := securechan.Config{Identity: localID, Expected: sessionPeer(activeSession)}
				go func(ctx context.Context, s *session.Channel) {
					defer printPrompt()
					if receiveOffer(ctx, addr, ".", 0, offer, cfg, tid) == nil { // 0: the receiver chooses
						s.Send(session.Message{Type: "complete", TransferID: offer.TransferID})
					}
				}(ctx, activeSession)
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
						fmt.Printf("  [%d] %s (%s:%d)%s\n", i+1, listedName(nodeTrust, p), p.HostIP, p.Port, codeStr)
					}
				}
			case "pair":
				args := []string{}
				if len(parts) == 2 {
					args = strings.Fields(parts[1])
				}
				var rawArg string
				show, usePIN, wantTrust := false, false, false
				for _, a := range args {
					switch a {
					case "--trust":
						wantTrust = true
					case "--show":
						show = true
					case "--pin":
						usePIN = true
					case "--once": // a session saves nothing anyway (PAIR-08)
					default:
						rawArg = strings.Trim(a, `'"`) // a URI pasted with its quotes
					}
				}
				if (usePIN || show) && rawArg == "" {
					if usePIN { // show a PIN; the other device types it (bible §6.2)
						addr := net.JoinHostPort(getLocalIP(), strconv.Itoa(nodePort))
						p, err := pairing.NewPIN(pairing.PINConfig{Show: pinScreen(os.Stdout,
							"pair --pin "+addr+"   (in xfer node; or: xfer pair --pin "+addr+")")})
						if err != nil {
							fmt.Printf("[-] %v\n", err)
							break
						}
						offerTrustOnPair.Store(wantTrust)
						nodePIN.Store(p)
						pinDemand.Reset() // the user pairs again: PINs on demand may be drawn again (#105)
					}
					if show {
						u, err := invite.renew(nodeID.Fingerprint, getLocalIP(), nodePort, wantTrust)
						if err != nil {
							fmt.Printf("[-] %v\n", err)
							break
						}
						fmt.Println(" On the other device, scan this QR code or run there:")
						fmt.Printf("   xfer pair '%s'   (or, in xfer node: pair <uri>)\n\n", u)
						discovery.PrintTerminalQR(u.String())
						fmt.Printf(" This device: %s   (valid %d s, for one device)\n", nodeID.Fingerprint.Short(), int(pairing.TTL.Seconds()))
					}
					break
				}
				if rawArg == "" {
					fmt.Println("[-] Usage: pair <index | IP[:port] | 6-digit code | medxfer://...> [--trust], pair --show [--trust],")
					fmt.Println("    or pair --pin [--trust] (show a PIN)")
					break
				}
				targetIP := rawArg
				targetPort := defaultPort
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
					// The code only finds the device; it pairs by a PIN below.
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
				} else if host, port, err := net.SplitHostPort(rawArg); err == nil {
					// IP:PORT, as the PIN screen and the sender show it: a node whose port
					// was taken listens on another one (CHAN-01).
					if p, err := strconv.Atoi(port); err == nil && p > 0 && p < 65536 {
						targetIP, targetPort = host, p
					}
				}

				if targetIP == "" {
					fmt.Println("[-] Target address could not be resolved.")
					break
				}

				target := net.JoinHostPort(targetIP, strconv.Itoa(targetPort))
				fmt.Printf("[*] Connecting to node at %s...\n", target)
				dialCfg := pairCfg
				if qr != nil {
					dialCfg.Expected, dialCfg.AllowUnknown = qr.Fingerprint, false // the QR code's key
				}
				conn, err := dialSecure(target, dialCfg)
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

				// A QR code's secret, or a device this node trusts (mutual trust is checked on
				// both sides, PAIR-09); otherwise a PIN on demand (PAIR-11).
				declares := pairing.Declares(nodeTrust, peerFP)
				byPIN := qr == nil && !declares
				var resp session.Message
				if !byPIN {
					req := session.Message{Type: "pair_request", DeviceName: myName, TrustsYou: declares}
					if qr != nil {
						req.Secret = pairing.EncodeSecret(qr.Secret) // proves we saw the QR code
					}
					_ = ch.Send(req)
					_ = conn.SetReadDeadline(time.Now().Add(15 * time.Second))
					resp, err = ch.Read()
					_ = conn.SetReadDeadline(time.Time{})
					if err == nil && qr == nil && resp.Type == pairing.TypeReject && resp.Code == pairing.CodePINRequired {
						fmt.Println("[*] This node trusts that device, but it no longer trusts this node: that gives nothing (pairing by PIN).")
						ch.Close()
						if conn, err = dialSecure(target, pairCfg); err != nil {
							fmt.Println("[-] Failed to connect:", err)
							break
						}
						peerFP = conn.PeerFingerprint()
						ch = session.NewChannel(conn)
						byPIN = true
					}
				}
				if byPIN {
					if err := pairing.RequestPIN(ch, conn); err != nil {
						if errors.Is(err, pairing.ErrRefused) {
							fmt.Println("[-] The other device shows no PIN (too many wrong ones?): try again later.")
						} else {
							fmt.Printf("[-] Pairing failed: %v\n", err)
						}
						ch.Close()
						break
					}
					// Typed here, never an argument: the PIN stays out of any history.
					_ = conn.SetDeadline(time.Now().Add(pairing.TTL))
					fmt.Print("PIN shown on the other device: ")
					var typed string
					select {
					case typed = <-inputChan:
					case <-time.After(pairing.TTL):
						fmt.Println()
					}
					if err := pairing.InitiatePIN(ch, conn, strings.TrimSpace(typed), nodeID.Fingerprint, myName); err != nil {
						if errors.Is(err, pairing.ErrRefused) {
							fmt.Println("[-] Pairing refused: check the PIN (a PIN changes after 3 wrong ones).")
						} else {
							fmt.Printf("[-] Pairing failed: %v\n", err)
						}
						ch.Close()
						break
					}
					_ = conn.SetReadDeadline(time.Now().Add(15 * time.Second))
					resp, err = ch.Read()
					_ = conn.SetDeadline(time.Time{})
				}
				if err != nil || resp.Type != "pair_accept" {
					reason := "refused by the other device"
					if err != nil {
						reason = err.Error()
					} else if resp.FileName != "" {
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
				if remoteName == "" {
					remoteName = targetIP
				}
				mutual := !byPIN && qr == nil && pairing.Mutual(nodeTrust, peerFP, resp.TrustsYou)
				startSession(ch, peerFP, remoteName, targetIP, mutual, wantTrust, false)
			case "trust":
				offerTrust()
			case "disconnect":
				if activeSession != nil {
					activeSession.Send(session.Message{Type: "disconnect"})
					activeSession.Close()
					activeSession = nil
					lastOffered.close() // its data connections end with the session
					lastOffered, incomingOffer, pendingOffer = nil, nil, nil
					endTalk()
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

				// One offer for a file, several files or a folder (bible §8.3).
				offer, files, err := transfer.BuildOffer(rawPaths, protocol.DefaultChunkSize)
				if err != nil {
					fmt.Printf("[-] Cannot send: %v\n", err)
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
				lastOffered.close()
				offer.TransferID = tid.String()
				lastOffered = &nodeOffer{offer: offer, files: files, l: tl}

				msg, follow := session.OfferMessages(session.Message{Type: "offer", DataPort: nodePort, TransferID: offer.TransferID}, offer)
				fmt.Printf("[*] Offering %s... Waiting for peer response.\n", offerSummary(offer))
				activeSession.Send(msg)
				for _, m := range follow {
					activeSession.Send(m)
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
					lastOffered.close() // its data connections end with the session
					lastOffered, incomingOffer, pendingOffer = nil, nil, nil
					endTalk()
				}
				if transferCancel != nil {
					transferCancel()
					transferCancel = nil
				}
				printPrompt()

			case "offer":
				if transferCancel != nil {
					transferCancel()
					transferCancel = nil
				}
				incomingOffer, pendingOffer = nil, nil
				if msg.Offer == nil {
					fmt.Println("\n[-] The peer sent an offer without its items.")
					printPrompt()
					break
				}
				m := msg
				incomingOffer = &m
				pendingOffer = takeNodeOffer(nil)

			case session.TypeOfferItems: // the next page of a paged offer (D2)
				if incomingOffer == nil || msg.TransferID != incomingOffer.Offer.TransferID {
					break
				}
				pendingOffer = takeNodeOffer(msg.Items)

			case "reject":
				lastOffered.close() // nobody may join a refused offer
				lastOffered = nil
				fmt.Println("\n[-] Peer rejected the file transfer.")
				printPrompt()

			case "accept":
				o := lastOffered
				lastOffered = nil
				if o == nil || msg.TransferID != o.offer.TransferID {
					fmt.Println("\n[-] The peer accepted an offer this node no longer makes.")
					o.close()
					printPrompt()
					break
				}
				fmt.Println("\n[+] Peer accepted! Sending...")
				var ctx context.Context
				ctx, transferCancel = context.WithCancel(context.Background())
				go func() {
					defer printPrompt()
					if err := runNodeServe(ctx, o); err != nil {
						fmt.Printf("\n[-] Transfer error: %v\n", err)
					}
				}()

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

			case pairing.TypeTrustOffer, pairing.TypeTrustAccept, pairing.TypeTrustDecline, pairing.TypeTrustCommit:
				t := talk.Load()
				if t == nil || activeSession == nil {
					break
				}
				reply, ev, err := t.Handle(msg)
				if err != nil {
					fmt.Printf("\n[-] Could not save the trusted device: %v\n", err)
					printPrompt()
					break
				}
				if reply != nil {
					_ = activeSession.Send(*reply)
				}
				switch ev {
				case pairing.TrustAsked:
					pendingTrustAsk = true
					fmt.Println("\n\n[?] The paired device offers mutual trust: both devices would then reconnect without a PIN.")
					printPrompt()
				case pairing.TrustEstablished:
					talkGen.Add(1)
					sessionMutual.Store(true)
					fmt.Println("\n[+] Mutual trust established, on both sides: next time, no PIN (xfer trust list).")
					printPrompt()
				case pairing.TrustDeclined:
					talkGen.Add(1)
					fmt.Println("\n[-] Mutual trust declined by the other device: the session stays temporary.")
					printPrompt()
				}

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
				lastOffered.close() // its data connections end with the session
				lastOffered, incomingOffer, pendingOffer = nil, nil, nil
				endTalk()
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

// nodeOffer is what a node offered its peer, until the peer accepts or rejects it.
type nodeOffer struct {
	offer protocol.Offer
	files []string
	l     *transfer.Listener // the offer's data connections, joinable by the peer only
}

// close withdraws the offer: nobody may join it any more.
func (o *nodeOffer) close() {
	if o != nil && o.l != nil {
		o.l.Close()
	}
}

// completeNodeOffer adds page to the offer being received and returns it once all its items
// are there and it is valid (LIM-05). A bad offer is dropped, with the error to report.
func completeNodeOffer(incoming **session.Message, page []protocol.OfferItem) (*session.Message, error) {
	m := *incoming
	if page != nil {
		if err := m.Offer.AddPage(page); err != nil {
			*incoming = nil
			return nil, err
		}
	}
	if !m.Offer.Complete() {
		return nil, nil
	}
	*incoming = nil
	m.Offer.Count = 0
	if err := m.Offer.Validate(); err != nil {
		return nil, err
	}
	return m, nil
}

// runNodeServe serves an accepted offer on its data connections, until the receiver has
// everything or ctx ends.
func runNodeServe(parentCtx context.Context, o *nodeOffer) error {
	ctx, cancel := context.WithCancel(parentCtx)
	defer cancel()
	defer o.close()
	sender, err := transfer.NewSender(o.offer, o.files)
	if err != nil {
		return err
	}
	sender.SetEvents(newCLIEvents(true, cancel))
	if err := sender.Serve(ctx, o.l); err != nil && ctx.Err() == nil {
		return err
	}
	return nil
}

// =====================================================================
// ONE-SHOT MODE (FILES & FOLDERS)
// =====================================================================
func handleSend(args []string) {
	sendCmd := flag.NewFlagSet("send", flag.ExitOnError)
	portFlag := sendCmd.Int("port", defaultPort, "TCP port of this device (pairing and data); if the default one is taken, a free one")
	chunkSizeMB := sendCmd.Int("chunk", protocol.DefaultChunkSize>>20, "Chunk size in MB (1 to 8)")
	createNetwork := sendCmd.Bool("create-network", false, "Create a dedicated Wi-Fi Direct network")
	pinFlag := sendCmd.Bool("pin", false, "show only the PIN, without the QR code")
	trustFlag := sendCmd.Bool("trust", false, "offer mutual trust to the receiver (both must accept)")
	var interactive bool
	sendCmd.BoolVar(&interactive, "i", false, "Require confirmation before sending")
	sendCmd.BoolVar(&interactive, "interactive", false, "Require confirmation before sending")

	_ = sendCmd.Parse(reorderArgs(sendCmd, args))
	if sendCmd.NArg() < 1 {
		fmt.Println("Error: Missing file or folder path. Usage: xfer send [-i] <path> [additional_paths...]")
		os.Exit(1)
	}
	if *chunkSizeMB < 1 || *chunkSizeMB > 8 {
		fmt.Println("Error: -chunk must be between 1 and 8 (MB).")
		os.Exit(1)
	}

	id, trust, err := loadIdentity(configDir())
	if err != nil {
		fmt.Printf("[-] %v\n", err)
		return
	}
	localID = id

	// One offer for a file, several files or a folder (bible §8.3).
	offer, files, err := transfer.BuildOffer(sendCmd.Args(), uint32(*chunkSizeMB)<<20)
	if err != nil {
		fmt.Printf("[-] Cannot send: %v\n", err)
		return
	}
	portSet := false
	sendCmd.Visit(func(f *flag.Flag) { portSet = portSet || f.Name == "port" })
	runOneShotSend(offer, files, *portFlag, !portSet, *createNetwork, interactive, *pinFlag, *trustFlag, trust)
}

// runOneShotSend offers files on the device's single port: the receiver pairs on it (QR code,
// or both users' confirmation), then pulls the whole offer on its data connections.
// anyPort lets it take a free port when port is taken (another medXfer on this device).
func runOneShotSend(offer protocol.Offer, files []string, port int, anyPort bool, createHotspot bool, interactive bool, usePIN, offerTrust bool, trust *identity.TrustStore) {
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
	hub, hubLn, err := startSendHub("0.0.0.0", port, anyPort, trust)
	if err != nil {
		fmt.Printf("[-] Cannot listen on port %d: %v\n", port, err)
		return
	}
	defer hubLn.Close()
	if got := hubLn.Addr().(*net.TCPAddr).Port; got != port {
		fmt.Printf("[!] Port %d is taken (another medXfer on this device?): using port %d.\n", port, got)
		fmt.Printf("    On Windows, the firewall must let this port in, or the receiver cannot download.\n")
		port = got
	}
	base, err := transfer.NewID()
	if err != nil {
		fmt.Printf("[-] %v\n", err)
		return
	}
	offer.TransferID = base.String()
	send := newOneShotSend(hub, base)
	if interactive {
		send.authorize = func(r pairing.Result) bool {
			fmt.Printf("\n[?] Receiver '%s' (%s) is paired.\n    Authorize sending %s? [Y/n]: ", r.Name, r.Address, offerSummary(offer))
			ans := strings.TrimSpace(strings.ToLower(askLine(ctx)))
			allowed := ans == "" || ans == "y" || ans == "yes" || ans == "o" || ans == "oui"
			if !allowed {
				fmt.Println("[-] Transfer aborted: declined by sender.")
			}
			return allowed
		}
	}

	// Discovery only says that a transfer waits (DISC-06).
	public := &discovery.TransferOffer{FileName: offer.Root, FileSize: offer.TotalBytes, IsBatch: len(offer.Items) > 1}
	pCode, _, _ := discovery.GeneratePairingCode(getLocalIP())
	discServer := discovery.NewDiscoveryServer("sender", port, public)
	discServer.SetTags(announceTagsOf(trust, localID.Fingerprint)) // devices of mutual trust recognize this one (DISC-11)
	discServer.SetPairingCode(pCode)
	discServer.Start(ctx)

	fmt.Println("==================================================")
	fmt.Println("             medXfer Secure Sender                ")
	fmt.Println("==================================================")
	fmt.Printf(" Sending      : %s\n", offerSummary(offer))
	fmt.Printf(" Fingerprint  : %s\n", localID.Fingerprint.Short())
	fmt.Printf(" Appears as   : %s   (the name in the receiver's list)\n", discServer.GetPeer().DeviceName)
	fmt.Printf(" Direct IP    : %s:%d\n", getLocalIP(), port)
	fmt.Printf(" Pairing Code : %s   (xfer recv %s: then type the PIN shown here)\n", pCode, pCode)
	fmt.Println("--------------------------------------------------")
	msg, follow := session.OfferMessages(session.Message{DataPort: port, TransferID: base.String()}, offer)
	startOneShotPairing(ctx, send, port, msg, follow, trust, usePIN, offerTrust)
	fmt.Println("[*] Waiting for receiver to connect...")

	tl := send.listener(ctx) // registered for the paired receiver only (CHAN-06)
	if tl == nil {
		return
	}
	sender, err := transfer.NewSender(offer, files)
	if err != nil {
		fmt.Printf("[-] %v\n", err)
		return
	}
	sender.SetEvents(newCLIEvents(true, cancel))
	if err := sender.Serve(ctx, tl); err != nil && ctx.Err() == nil {
		fmt.Printf("\n[-] Transfer error: %v\n", err)
	}
}

func handleRecv(args []string) {
	recvCmd := flag.NewFlagSet("recv", flag.ExitOnError)
	ipFlag := recvCmd.String("ip", "", "Direct sender address")
	codeFlag := recvCmd.String("code", "", "6-digit pairing code (e.g. 229-481)")
	outDirFlag := recvCmd.String("out", ".", "Directory to save files")
	workersFlag := recvCmd.Int("workers", 0, "data connections, 1 to 8; 0 chooses: 4, or 8 for many small files")
	pinFlag := recvCmd.Bool("pin", false, "type the PIN shown by the sender at HOST[:PORT] (or --ip)")
	trustFlag := recvCmd.Bool("trust", false, "offer mutual trust to the sender (both must accept)")

	_ = recvCmd.Parse(reorderArgs(recvCmd, args))

	// The sender's PIN (xfer send --pin), for a device without a camera.
	if *pinFlag {
		addr := *ipFlag
		if addr == "" && recvCmd.NArg() > 0 {
			addr = recvCmd.Arg(0)
		}
		if addr == "" {
			fmt.Println("[-] Give the address the sender shows: xfer recv --pin HOST[:PORT]")
			return
		}
		recvFromPIN(addr, *outDirFlag, *workersFlag, *trustFlag)
		return
	}

	// The sender's QR code: its key comes from it, with the secret that lets this device in.
	if recvCmd.NArg() > 0 && strings.HasPrefix(recvCmd.Arg(0), "medxfer://") {
		recvFromURI(recvCmd.Arg(0), *outDirFlag, *workersFlag, *trustFlag)
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
	senderHost := selectedPeer.HostIP
	pairAddr := net.JoinHostPort(senderHost, strconv.Itoa(selectedPeer.Port)) // the sender's single port
	fmt.Printf("[*] Connecting to %s...\n", pairAddr)
	o := recvOptions(id, trust, *trustFlag)

	// A device of mutual trust gets in at once (P3); any other pairs by the PIN the sender
	// shows (PAIR-11, PAIR-12): never on fingerprints compared by eye.
	keyNote := "a device of mutual trust"
	r, err := pairing.JoinTrusted(context.Background(), pairAddr, o)
	if errors.Is(err, pairing.ErrPINRequired) {
		if known, changed := keyChanged(trust, senderHost, r.Fingerprint); changed {
			fmt.Println(keyChangedAlert(known, senderHost, r.Fingerprint))
			return
		}
		if trust != nil && trust.Contains(r.Fingerprint) {
			fmt.Println("[*] This device trusts the sender, but the sender no longer trusts it: that gives nothing (pairing by PIN).")
		}
		keyNote = "proved by the PIN"
		r, err = pairing.JoinPINOnDemand(context.Background(), pairAddr, typePIN("the sender"), o)
	}
	if errors.Is(err, pairing.ErrRefused) {
		fmt.Println("[-] Refused: check the PIN (it changes after 3 wrong ones).")
		return
	}
	if err != nil {
		fmt.Printf("[-] Pairing with the sender failed: %v\n", err)
		return
	}
	trustOutcome(os.Stdout, r, *trustFlag)
	receiveAccepted(r, senderHost, keyNote, *outDirFlag, *workersFlag)
}

// recvOptions are the pairing options of xfer recv: this device's key and list, and mutual
// trust when asked (--trust) or offered by the sender (PAIR-10).
func recvOptions(id *identity.Identity, trust *identity.TrustStore, offerTrust bool) pairing.Options {
	return pairing.Options{Identity: id, Trust: trust, DeviceName: api.DeviceName(), OfferTrust: offerTrust,
		AskTrust: askTrustFrom(os.Stdout, stdinLines())}
}

// typePIN asks the user for the PIN that who shows: never an argument, it would stay in the
// shell history (bible chapter 13).
func typePIN(who string) func() (string, error) {
	return func() (string, error) {
		fmt.Printf("PIN shown by %s: ", who)
		l, ok := <-stdinLines()
		if !ok {
			return "", errors.New("no terminal to type the PIN")
		}
		return strings.TrimSpace(l), nil
	}
}

// readLine is the next line typed on the terminal, or "" without one.
func readLine() string {
	l, _ := <-stdinLines()
	return l
}

// recvFromURI receives a one-shot send from its QR code: the sender's key comes from it, so
// there is no trust question, and nothing is saved (PAIR-08).
func recvFromURI(raw, outDir string, workers int, offerTrust bool) {
	u, err := pairing.Parse(raw, time.Now())
	if errors.Is(err, pairing.ErrExpired) {
		fmt.Println("[-] This QR code has expired: use the one the sender shows now.")
		return
	}
	if err != nil {
		fmt.Printf("[-] %v\n", err)
		return
	}
	id, trust, err := loadIdentity(configDir())
	if err != nil {
		fmt.Printf("[-] %v\n", err)
		return
	}
	localID = id
	fmt.Printf("[*] Connecting to %s (key %s)...\n", u.Addr(), u.Fingerprint.Short())
	r, err := pairing.Join(context.Background(), u, recvOptions(id, trust, offerTrust))
	if err != nil {
		fmt.Printf("[-] The sender refused this device: %v\n", err)
		return
	}
	trustOutcome(os.Stdout, r, offerTrust)
	receiveAccepted(r, u.Host, "from the QR code", outDir, workers)
}

// recvFromPIN receives a one-shot send by the PIN its sender shows (bible §6.2): the PIN
// proves the sender, so there is no trust question, and nothing is saved (PAIR-08).
func recvFromPIN(addr, outDir string, workers int, offerTrust bool) {
	if _, _, err := net.SplitHostPort(addr); err != nil {
		addr = net.JoinHostPort(addr, strconv.Itoa(defaultPort))
	}
	id, trust, err := loadIdentity(configDir())
	if err != nil {
		fmt.Printf("[-] %v\n", err)
		return
	}
	localID = id
	fmt.Printf("[*] Connecting to %s...\n", addr)
	// Asking for the PIN gets the one on the sender's screen (PAIR-11).
	r, err := pairing.JoinPINOnDemand(context.Background(), addr, typePIN("the sender"), recvOptions(id, trust, offerTrust))
	if errors.Is(err, pairing.ErrRefused) {
		fmt.Println("[-] Refused: check the PIN (it changes after 3 wrong ones) and the address.")
		return
	}
	if err != nil {
		fmt.Printf("[-] %v\n", err)
		return
	}
	host, _, _ := net.SplitHostPort(addr)
	trustOutcome(os.Stdout, r, offerTrust)
	receiveAccepted(r, host, "proved by the PIN", outDir, workers)
}

// receiveAccepted shows the offer that came with pair_accept, inside the secure channel, and
// downloads it if the user confirms. Every data connection is pinned to the key the pairing
// verified and joins the transfer (CHAN-06).
func receiveAccepted(r pairing.Result, host, keyNote string, outDir string, workers int) {
	m := r.Offer
	if m.Offer == nil || m.DataPort == 0 {
		fmt.Println("[-] This device offers no transfer (to pair, run: xfer pair <uri>).")
		return
	}
	offer := *m.Offer
	base, err := transfer.ParseID(offer.TransferID)
	if err != nil {
		fmt.Printf("[-] Invalid offer: %v\n", err)
		return
	}
	targetAddr := net.JoinHostPort(host, strconv.Itoa(m.DataPort))
	cfg := securechan.Config{Identity: localID, Expected: r.Fingerprint}

	printIncoming(offer, r.Name, targetAddr)
	fmt.Printf(" Key    : %s (%s)\n", r.Fingerprint.Short(), keyNote)
	fmt.Println("--------------------------------------------------")
	if err := checkSpace(outDir, offer, r.Fingerprint); err != nil {
		fmt.Printf("[-] Cannot receive into %s: %v.\n", outDir, err)
		return
	}
	fmt.Print("[?] Confirm download? [Y/n]: ")
	ans := strings.TrimSpace(strings.ToLower(readLine()))
	if ans != "" && ans != "y" && ans != "yes" && ans != "o" && ans != "oui" {
		fmt.Println("[-] Download cancelled by user.")
		return
	}
	pullOffer(targetAddr, outDir, workers, offer, cfg, base)
}

// printIncoming shows what the sender offers.
func printIncoming(o protocol.Offer, from, addr string) {
	fmt.Println("\n==================================================")
	fmt.Println("             medXfer Incoming Transfer            ")
	fmt.Println("==================================================")
	if len(o.Items) == 1 {
		fmt.Printf(" File   : %s (%.2f MB)\n", o.Items[0].Path, float64(o.TotalBytes)/(1024*1024))
	} else {
		fmt.Printf(" Folder : %s\n", o.Root)
		fmt.Printf(" Files  : %d\n", len(o.Items))
		fmt.Printf(" Size   : %.2f MB\n", float64(o.TotalBytes)/(1024*1024))
	}
	fmt.Printf(" From   : %s (%s)\n", from, addr)
}

// pullOffer downloads the whole offer from targetAddr, the sender's single port: one transfer,
// every data connection pinned to the sender's key (cfg) and joining it (bible §8.3).
// Enter on the terminal resumes a transfer paused on a full disk.
func pullOffer(targetAddr, outDir string, workers int, offer protocol.Offer, cfg securechan.Config, base transfer.ID) {
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

	go func() {
		for {
			if _, ok := <-stdinLines(); !ok {
				return
			}
			if resume := takeDiskResume(); resume != nil {
				fmt.Println("[*] Resuming...")
				resume()
			}
		}
	}()

	fmt.Printf("\n[*] Connecting to sender at %s...\n", targetAddr)
	_ = receiveOffer(ctx, targetAddr, outDir, workers, offer, cfg, base)
}

// receiveOffer downloads offer from targetAddr, the sender's single port, and tells the user
// how it went: one transfer, every data connection pinned to the sender's key (cfg) and joining
// it (bible §8.3). It returns nil only when every item was received.
func receiveOffer(ctx context.Context, targetAddr, outDir string, workers int, offer protocol.Offer, cfg securechan.Config, base transfer.ID) error {
	rx := transfer.NewReceiver(outDir, transfer.JoinFunc(cfg, base))
	rx.SenderKey = cfg.Expected // part of each file's resume key (D2)
	rx.Workers = workers
	events := newCLIEvents(false, nil)
	events.resume = rx.Resume
	rx.Events = events
	start := time.Now()
	results, err := rx.Run(ctx, targetAddr, offer)
	events.done()
	if err != nil {
		if ctx.Err() == nil {
			fmt.Printf("\n[-] Download failed: %v\n", err)
			if errors.Is(err, transfer.ErrTransferFailed) {
				fmt.Print(unreachableSender(targetAddr))
			}
		}
		return err
	}
	failed := 0
	for _, res := range results {
		if res.State != transfer.ItemDone {
			failed++
		}
	}
	if failed > 0 {
		fmt.Printf("\n[-] %d of %d files could not be received.\n", failed, len(results))
		return fmt.Errorf("%d of %d files not received", failed, len(results))
	}
	fmt.Printf("\n[+] Transfer completed in %s\n", time.Since(start).Round(time.Millisecond))
	if len(results) > 1 {
		fmt.Printf("[+] All %d files saved.\n", len(results))
	}
	events.printLimiting()
	return nil
}

// unreachableSender explains a download whose data connections could not reach the sender
// (#133): the usual cause is the sender's firewall, Windows above all.
func unreachableSender(addr string) string {
	_, port, _ := net.SplitHostPort(addr)
	return fmt.Sprintf("    The sender could not be reached at %s. If it is a Windows PC, its firewall must let\n"+
		"    TCP port %s in (see the guide, section 9). Then send again.\n", addr, port)
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
	_, trust, _ := loadIdentity(configDir()) // to recognize trusted devices in the list
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
				fmt.Printf("  [%d] %s (%s) - transfer waiting\n", i+1, listedName(trust, s), s.HostIP)
			}
		}
		fmt.Print("Select an option or enter pairing code [r to refresh, q to quit]: ")
		input := strings.TrimSpace(readLine())
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
	daemonCmd := flag.NewFlagSet("daemon", flag.ExitOnError)
	portFlag := daemonCmd.Int("port", 19999, "Port for Headless API (HTTP & WebSocket)")
	outDirFlag := daemonCmd.String("out", "", "Default directory to save incoming files")
	nameFlag := daemonCmd.String("name", "", "Custom device name for discovery")
	webShareFlag := daemonCmd.Bool("web-share", false, "Open the Web Share guest portal to the LAN at startup")

	_ = daemonCmd.Parse(reorderArgs(daemonCmd, args))
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
	fmt.Println(" Web Dashboard (development build, this device only):") // devui builds only (WP-6.6)
	fmt.Printf("   👉 %s/\n", localURL)
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
	shareCmd := flag.NewFlagSet("share", flag.ExitOnError)
	portFlag := shareCmd.Int("port", 19999, "Port for Web Share HTTP portal")
	pinFlag := shareCmd.String("pin", "", "Custom 4-digit security PIN (default: random)")
	autoAcceptFlag := shareCmd.Bool("auto-accept", false, "Auto-accept incoming uploads without terminal prompt")
	outDirFlag := shareCmd.String("out", "", "Directory to save incoming uploaded files")
	nameFlag := shareCmd.String("name", "", "Custom device name")

	_ = shareCmd.Parse(reorderArgs(shareCmd, args))
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
		"  xfer daemon | dev-ui [--port 19999] [--web-share]  (Web dashboard daemon; devui builds only)\n" +
		"  xfer node                                      (Persistent interactive CLI: QR code or fingerprints)\n" +
		"  xfer share <file_or_folder...>                 (Instant Web Share with QR code & PIN)\n" +
		"  xfer send [-i] <file_or_folder> [more...]      (Direct transfer: shows a QR code; -i prompts)\n" +
		"  xfer recv 'medxfer://...'                      (Receive from the sender's QR code)\n" +
		"  xfer recv [pairing_code]                       (Via 6-digit code or discovery; both confirm)\n" +
		"  xfer recv --ip <addr>                          (Direct connect by IP)\n" +
		"  xfer send --pin <path> | xfer recv --pin HOST  (A PIN instead of the QR code)\n" +
		"  xfer report show|compare|list [--json]         (Transfer diagnostic reports)\n" +
		"  xfer ctl [--script scenario.jsonl]             (Drive an embedded daemon with JSON lines)\n" +
		"  xfer bench disk|net [...]                      (Disk and network benchmarks)\n" +
		"  xfer bundle [-o FILE.zip]                      (Diagnostic bundle for a bug report)\n" +
		"  xfer id                                        (This device's fingerprint)\n" +
		"  xfer trust list|forget|rename <fingerprint>    (Trusted devices)\n" +
		"  xfer name [NAME]                               (This device's name, sent once paired)\n" +
		"  xfer pair --show | xfer pair <medxfer://...>   (Pair two devices by QR code)\n" +
		"  xfer pair --pin | xfer pair --pin HOST[:PORT]  (Pair two devices by PIN)\n" +
		"\n" +
		"Examples on Android / Termux:\n" +
		"  ./xfer-android-arm64 daemon                    -> Starts Web UI on http://localhost:19999\n" +
		"  ./xfer-android-arm64 node                      -> Interactive CLI (scan, pair, send, recv)\n" +
		"  ./xfer-android-arm64 share /sdcard/video.mp4   -> Instant Web Share with QR Code\n" +
		"==================================================\n")
}
