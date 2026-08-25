package main

import (
	"bufio"
	"context"
	"flag"
	"fmt"
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
	"github.com/Medboy224/medXfer/pkg/ui"
)

const defaultPort = 18888

// 1. Program Entry Point
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
	default:
		fmt.Printf("Unknown command: %s\n\n", os.Args[1])
		printUsage()
		os.Exit(1)
	}
}

// reorderArgs moves all flags (-flag, --flag) ahead of positional arguments
// so Go doesn't stop parsing when it sees the file path.
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

// 2. Sender Handler
func handleSend(args []string) {
	normalizedArgs := reorderArgs(args)

	sendCmd := flag.NewFlagSet("send", flag.ExitOnError)
	portFlag := sendCmd.Int("port", defaultPort, "TCP port to bind sender on")
	workersFlag := sendCmd.Int("workers", 4, "Number of parallel TCP streams")
	chunkSizeMB := sendCmd.Int("chunk", 2, "Chunk slice size in MB (Default 2MB for stability)")
	createNetwork := sendCmd.Bool("create-network", false, "Create a dedicated Wi-Fi Direct network (Windows)")

	_ = sendCmd.Parse(normalizedArgs)

	if sendCmd.NArg() < 1 {
		fmt.Println("Error: Missing file path.")
		fmt.Println("Usage: xfer send <file_path> [--create-network] [--workers 4]")
		os.Exit(1)
	}

	filePath := sendCmd.Arg(0)
	info, err := os.Stat(filePath)
	if err != nil {
		fmt.Printf("[-] Cannot access file '%s': %v\n", filePath, err)
		os.Exit(1)
	}

	fileName := filepath.Base(filePath)
	fileSize := info.Size()

	var netInfo *hotspot.NetworkInfo
	var hs hotspot.Controller

	// Step A: Initialize Wi-Fi Direct if --create-network is requested
	if *createNetwork {
		hs = hotspot.New()
		ssid, pass := hotspot.GenerateCredentials()

		fmt.Println("[*] Creating dedicated Wi-Fi Direct network...")
		var err error
		netInfo, err = hs.Start(hotspot.Config{
			SSID:     ssid,
			Password: pass,
			Band:     hotspot.Band5GHz,
		})

		if err != nil {
			fmt.Printf("[-] Network Creation Failed: %v\n", err)
			fmt.Println("[*] Falling back to existing network adapter.")
		} else {
			defer func() {
				fmt.Println("\n[*] Tearing down Wi-Fi Direct network...")
				_ = hs.Stop()
			}()

			fmt.Println("==================================================")
			fmt.Printf(" [NETWORK READY] %s (%s, Channel %d)\n", netInfo.SSID, netInfo.Band, netInfo.Channel)
			fmt.Printf(" Password: %s\n", netInfo.Password)
			fmt.Println("==================================================")
			fmt.Println("Scan to connect your phone:")
			discovery.PrintHotspotQR(netInfo.SSID, netInfo.Password)
			fmt.Println()
		}
	}

	// Step B: Setup graceful shutdown
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	sigChan := make(chan os.Signal, 1)
	signal.Notify(sigChan, os.Interrupt, syscall.SIGTERM)
	go func() {
		<-sigChan
		fmt.Println("\n[*] Cancellation received, stopping...")
		cancel()
		if hs != nil {
			_ = hs.Stop()
		}
		os.Exit(0)
	}()

	// Step C: Start Discovery Server
	offer := &discovery.TransferOffer{
		FileName: fileName,
		FileSize: fileSize,
	}
	discServer := discovery.NewDiscoveryServer("sender", *portFlag, offer)
	discServer.Start(ctx)

	bindAddr := fmt.Sprintf("0.0.0.0:%d", *portFlag)

	fmt.Println("==================================================")
	fmt.Printf(" [OFFERING FILE] %s (%.2f MB)\n", fileName, float64(fileSize)/(1024*1024))
	fmt.Println("==================================================")

	if netInfo == nil {
		primaryIP := discovery.GetPrimaryLocalIP()
		fmt.Printf(" Listening on: %s:%d\n", primaryIP, *portFlag)
		fmt.Println(" Pairing QR:")
		discovery.PrintTerminalQR(fmt.Sprintf("%s:%d", primaryIP, *portFlag))
	} else {
		fmt.Println("[*] Waiting for phone to connect to Wi-Fi and discover sender...")
	}

	// Step D: Stream File over Parallel Engine
	chunkSize := uint32(*chunkSizeMB * 1024 * 1024)
	if chunkSize == 0 {
		chunkSize = 2 * 1024 * 1024 // Fallback to 2MB if user inputs 0
	}

	sender := engine.NewSender(*workersFlag, chunkSize)
	bar := ui.NewProgressBar()

	err = sender.ServeAndSend(bindAddr, filePath, func(current, total int64, speed float64) {
		bar.Render(current, total, speed)
	})

	if err != nil {
		fmt.Printf("\n[-] Transfer error: %v\n", err)
		os.Exit(1)
	}
	bar.Finish()
}

// 3. Receiver Handler
func handleRecv(args []string) {
	normalizedArgs := reorderArgs(args)

	recvCmd := flag.NewFlagSet("recv", flag.ExitOnError)
	ipFlag := recvCmd.String("ip", "", "Direct sender address (e.g. 192.168.137.1:18888)")
	outDirFlag := recvCmd.String("out", ".", "Directory to save received files")
	workersFlag := recvCmd.Int("workers", 4, "Number of parallel TCP streams")

	_ = recvCmd.Parse(normalizedArgs)

	var targetAddr string

	if *ipFlag != "" {
		targetAddr = *ipFlag
		if !strings.Contains(targetAddr, ":") {
			targetAddr = fmt.Sprintf("%s:%d", targetAddr, defaultPort)
		}
	} else {
		reader := bufio.NewReader(os.Stdin)
		for {
			fmt.Println("\n[*] Searching local network for medXfer senders...")
			peers, err := discovery.DiscoverPeers(2 * time.Second)
			if err != nil {
				fmt.Printf("[-] Discovery error: %v\n", err)
			}

			var senders []discovery.Peer
			for _, p := range peers {
				if p.Role == "sender" && p.Offer != nil {
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
			fmt.Println(" [q] Quit")
			fmt.Println("--------------------------------------------------")
			fmt.Print("Select an option: ")

			input, _ := reader.ReadString('\n')
			input = strings.TrimSpace(input)

			if strings.EqualFold(input, "q") {
				fmt.Println("Exiting.")
				os.Exit(0)
			} else if strings.EqualFold(input, "r") {
				continue
			} else if strings.EqualFold(input, "m") {
				fmt.Print("Enter sender IP:Port: ")
				manualInput, _ := reader.ReadString('\n')
				manualInput = strings.TrimSpace(manualInput)
				if !strings.Contains(manualInput, ":") {
					manualInput = fmt.Sprintf("%s:%d", manualInput, defaultPort)
				}
				targetAddr = manualInput
				break
			} else if num, err := strconv.Atoi(input); err == nil && num >= 1 && num <= len(senders) {
				selected := senders[num-1]
				targetAddr = fmt.Sprintf("%s:%d", selected.HostIP, selected.Port)
				fmt.Printf("\n[+] Selected [%s] offering '%s'\n", selected.DeviceName, selected.Offer.FileName)
				break
			} else {
				fmt.Println("[-] Invalid selection.")
			}
		}
	}

	fmt.Printf("[*] Connecting to sender at %s...\n", targetAddr)
	receiver := engine.NewReceiver(*outDirFlag, *workersFlag)
	bar := ui.NewProgressBar()

	err := receiver.Pull(targetAddr, func(current, total int64, speed float64) {
		bar.Render(current, total, speed)
	})

	if err != nil {
		fmt.Printf("\n[-] Download failed: %v\n", err)
		os.Exit(1)
	}
	bar.Finish()
}

// 4. Usage Documentation
func printUsage() {
	fmt.Printf("medXfer - High-Speed Peer-to-Peer CLI File Transfer\n\n")
	fmt.Printf("Usage:\n")
	fmt.Printf("  xfer send <file_path> [--create-network] [--workers 4]\n")
	fmt.Printf("  xfer recv [--out ./downloads] [--ip <sender_ip:port>]\n\n")
}
