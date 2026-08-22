package main

import (
	"bufio"
	"context"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/Medboy224/medXfer/pkg/discovery"
	"github.com/Medboy224/medXfer/pkg/engine"
	"github.com/Medboy224/medXfer/pkg/ui"
)

const defaultPort = 18888

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

func handleSend(args []string) {
	sendCmd := flag.NewFlagSet("send", flag.ExitOnError)
	portFlag := sendCmd.Int("port", defaultPort, "TCP port to bind sender on")
	workersFlag := sendCmd.Int("workers", 4, "Number of parallel TCP streams")
	chunkSizeMB := sendCmd.Int("chunk", 4, "Chunk slice size in MB")
	hotspotSSID := sendCmd.String("hotspot-ssid", "", "Print Wi-Fi Hotspot join QR code for this SSID")
	hotspotPass := sendCmd.String("hotspot-pass", "", "Wi-Fi Hotspot password")

	_ = sendCmd.Parse(args)

	if sendCmd.NArg() < 1 {
		fmt.Println("Error: Missing file path.")
		fmt.Println("Usage: xfer send <file_path> [--port 18888]")
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
	localIP := discovery.GetLocalIP()
	bindAddr := fmt.Sprintf("0.0.0.0:%d", *portFlag)

	// Start dual-mode discovery server (TCP sweep responder + UDP beacon)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	discovery.StartDiscoveryServer(ctx, fileName, fileSize, *portFlag)

	fmt.Println("==================================================")
	fmt.Printf(" [SENDER READY] Offering: %s (%.2f MB)\n", fileName, float64(fileSize)/(1024*1024))
	fmt.Printf(" Local Endpoint: %s:%d\n", localIP, *portFlag)
	fmt.Println("==================================================")

	if *hotspotSSID != "" {
		fmt.Println("\nScan with receiver phone to join Wi-Fi Hotspot:")
		discovery.PrintHotspotQR(*hotspotSSID, *hotspotPass)
		fmt.Println()
	}

	fmt.Println("Pairing QR Code (Receiver IP:Port):")
	discovery.PrintTerminalQR(fmt.Sprintf("%s:%d", localIP, *portFlag))
	fmt.Println("\n[*] Waiting for receiver to connect and pull file...")

	chunkSize := uint32(*chunkSizeMB * 1024 * 1024)
	sender := engine.NewSender(*workersFlag, chunkSize)
	bar := ui.NewProgressBar()

	// Uses ServeAndSend matching pkg/engine/sender.go
	err = sender.ServeAndSend(bindAddr, filePath, func(current, total int64, speed float64) {
		bar.Render(current, total, speed)
	})

	if err != nil {
		fmt.Printf("\n[-] Transfer error: %v\n", err)
		os.Exit(1)
	}
	bar.Finish()
}

func handleRecv(args []string) {
	recvCmd := flag.NewFlagSet("recv", flag.ExitOnError)
	ipFlag := recvCmd.String("ip", "", "Direct sender address (e.g. 192.168.43.1:18888)")
	outDirFlag := recvCmd.String("out", ".", "Directory to save the received file")
	workersFlag := recvCmd.Int("workers", 4, "Number of parallel TCP streams")

	_ = recvCmd.Parse(args)

	var targetAddr string

	if *ipFlag != "" {
		targetAddr = *ipFlag
		if !strings.Contains(targetAddr, ":") {
			targetAddr = fmt.Sprintf("%s:%d", targetAddr, defaultPort)
		}
	} else {
		// Peer scanning loop matching pkg/discovery/udp.go
		reader := bufio.NewReader(os.Stdin)
		for {
			fmt.Println("\n[*] Scanning local network for active senders...")
			offers, err := discovery.ScanForSenders(2500 * time.Millisecond)
			if err != nil {
				fmt.Printf("[-] Discovery scan error: %v\n", err)
			}

			fmt.Println("==================================================")
			fmt.Println("       medXfer - Available Senders on LAN         ")
			fmt.Println("==================================================")

			if len(offers) == 0 {
				fmt.Println(" (No active senders found on local network)")
			} else {
				for i, off := range offers {
					sizeMB := float64(off.FileSize) / (1024 * 1024)
					fmt.Printf(" [%d] %s (%s:%d)\n", i+1, off.DeviceName, off.HostIP, off.Port)
					fmt.Printf("     File: %s (%.2f MB)\n\n", off.FileName, sizeMB)
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
				fmt.Print("Enter sender IP:Port (e.g. 192.168.43.15:18888): ")
				manualInput, _ := reader.ReadString('\n')
				manualInput = strings.TrimSpace(manualInput)
				if !strings.Contains(manualInput, ":") {
					manualInput = fmt.Sprintf("%s:%d", manualInput, defaultPort)
				}
				targetAddr = manualInput
				break
			} else if num, err := strconv.Atoi(input); err == nil && num >= 1 && num <= len(offers) {
				selected := offers[num-1]
				targetAddr = fmt.Sprintf("%s:%d", selected.HostIP, selected.Port)
				fmt.Printf("\n[+] Selected [%s] offering '%s'\n", selected.DeviceName, selected.FileName)
				break
			} else {
				fmt.Println("[-] Invalid selection, please try again.")
			}
		}
	}

	fmt.Printf("[*] Connecting to sender at %s...\n", targetAddr)
	receiver := engine.NewReceiver(*outDirFlag, *workersFlag)
	bar := ui.NewProgressBar()

	// Uses Pull matching pkg/engine/receiver.go
	err := receiver.Pull(targetAddr, func(current, total int64, speed float64) {
		bar.Render(current, total, speed)
	})

	if err != nil {
		fmt.Printf("\n[-] Download failed: %v\n", err)
		os.Exit(1)
	}
	bar.Finish()
}

func printUsage() {
	fmt.Printf("medXfer - High-Speed Peer-to-Peer CLI File Transfer\n\n")
	fmt.Printf("Usage:\n")
	fmt.Printf("  xfer send <file_path> [--port 18888] [--workers 4]\n")
	fmt.Printf("  xfer recv [--out ./downloads] [--ip <sender_ip:port>]\n\n")
}
