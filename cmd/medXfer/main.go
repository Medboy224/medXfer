package main

import (
	"context"
	"flag"
	"fmt"
	"os"
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
	ipFlag := sendCmd.String("ip", "", "Receiver IP address (leave empty for LAN auto-discovery)")
	portFlag := sendCmd.Int("port", defaultPort, "Receiver TCP port")
	workersFlag := sendCmd.Int("workers", 4, "Number of parallel TCP streams (2-8)")
	chunkSizeMB := sendCmd.Int("chunk", 4, "Chunk slice size in MB")

	_ = sendCmd.Parse(args)

	if sendCmd.NArg() < 1 {
		fmt.Println("Error: Missing file path to send.")
		fmt.Println("Usage: xfer send <file_path> [--ip <receiver_ip>]")
		os.Exit(1)
	}

	filePath := sendCmd.Arg(0)
	targetIP := *ipFlag
	targetPort := *portFlag

	// Auto-discovery if no IP was provided
	if targetIP == "" {
		fmt.Println("[*] Scanning local network for an active receiver...")
		discoveredIP, discoveredPort, err := discovery.DiscoverReceiver(4 * time.Second)
		if err != nil {
			fmt.Printf("[-] Auto-discovery failed: %v\n", err)
			fmt.Println("[-] Please provide the receiver IP manually with --ip <address>")
			os.Exit(1)
		}
		targetIP = discoveredIP
		targetPort = discoveredPort
		fmt.Printf("[+] Found receiver at %s:%d\n", targetIP, targetPort)
	}

	targetAddr := fmt.Sprintf("%s:%d", targetIP, targetPort)
	fmt.Printf("[*] Connecting to %s with %d parallel TCP streams...\n", targetAddr, *workersFlag)

	chunkSize := uint32(*chunkSizeMB * 1024 * 1024)
	sender := engine.NewSender(*workersFlag, chunkSize)
	bar := ui.NewProgressBar()

	err := sender.Transfer(filePath, targetAddr, func(current, total int64, speed float64) {
		bar.Render(current, total, speed)
	})

	if err != nil {
		fmt.Printf("\n[-] Transfer failed: %v\n", err)
		os.Exit(1)
	}
	bar.Finish()
}

func handleRecv(args []string) {
	recvCmd := flag.NewFlagSet("recv", flag.ExitOnError)
	portFlag := recvCmd.Int("port", defaultPort, "Port to listen on")
	outDirFlag := recvCmd.String("out", ".", "Directory to save received files")
	workersFlag := recvCmd.Int("workers", 4, "Expected parallel TCP streams")

	_ = recvCmd.Parse(args)

	localIP := discovery.GetLocalIP()
	listenAddr := fmt.Sprintf("0.0.0.0:%d", *portFlag)

	// Start background UDP beacon for auto-discovery
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go discovery.BroadcastReceiverBeacon(ctx, *portFlag)

	fmt.Println("==================================================")
	fmt.Printf(" [RECEIVER READY] Listening on %s:%d\n", localIP, *portFlag)
	fmt.Printf(" Saving files to: %s\n", *outDirFlag)
	fmt.Println("==================================================")
	fmt.Println("To pair directly from another device, run:")
	fmt.Printf("  xfer send <file> --ip %s\n\n", localIP)
	fmt.Println("Or scan this QR code on the sending terminal:")
	discovery.PrintTerminalQR(fmt.Sprintf("%s:%d", localIP, *portFlag))

	receiver := engine.NewReceiver(*outDirFlag, *workersFlag)
	bar := ui.NewProgressBar()

	err := receiver.ListenAndReceive(listenAddr, func(current, total int64, speed float64) {
		bar.Render(current, total, speed)
	})

	if err != nil {
		fmt.Printf("\n[-] Error receiving file: %v\n", err)
		os.Exit(1)
	}
	bar.Finish()
}

func printUsage() {
	fmt.Printf("medXfer - High-Speed Cross-Platform CLI File Transfer\n\n")
	fmt.Printf("Usage:\n")
	fmt.Printf("  xfer recv [--port 18888] [--out ./downloads]\n")
	fmt.Printf("  xfer send <filepath> [--ip <receiver_ip>] [--workers 4]\n\n")
}
