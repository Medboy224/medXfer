//go:build windows

package hotspot

import (
	"bufio"
	"context"
	"fmt"
	"io"
	"net"
	"os/exec"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/Medboy224/medXfer/pkg/discovery"
)

type windowsHotspot struct {
	mu      sync.Mutex
	cmd     *exec.Cmd
	stdin   io.WriteCloser
	stopped bool
	done    chan struct{}
}

func newController() Controller {
	return &windowsHotspot{
		done: make(chan struct{}),
	}
}

func (w *windowsHotspot) Start(cfg Config) (*NetworkInfo, error) {
	w.mu.Lock()
	defer w.mu.Unlock()

	if cfg.SSID == "" || cfg.Password == "" {
		cfg.SSID, cfg.Password = GenerateCredentials()
	}

	script := fmt.Sprintf(`
$ErrorActionPreference = 'Stop'
try {
    [Windows.Devices.WiFiDirect.WiFiDirectAdvertisementPublisher, Windows.Devices.WiFiDirect, ContentType = WindowsRuntime] | Out-Null
    $pub = New-Object Windows.Devices.WiFiDirect.WiFiDirectAdvertisementPublisher
    $pub.Advertisement.IsAutonomousGroupOwnerEnabled = $true
    $pub.Advertisement.LegacySettings.IsEnabled = $true
    $pub.Advertisement.LegacySettings.Ssid = '%s'
    $pub.Advertisement.LegacySettings.Passphrase.Password = '%s'
    $pub.Start()
    Write-Output "HOTSPOT_ONLINE"
    
    # Block until stdin receives EOF or line from parent process
    [Console]::In.ReadLine() | Out-Null
    
    $pub.Stop()
    Write-Output "HOTSPOT_STOPPED"
} catch {
    Write-Error $_.Exception.Message
    exit 1
}
`, cfg.SSID, cfg.Password)

	w.cmd = exec.Command("powershell", "-NoProfile", "-NonInteractive", "-Command", script)
	w.cmd.SysProcAttr = &syscall.SysProcAttr{
		CreationFlags: 0x08000000, // CREATE_NO_WINDOW
		HideWindow:    true,
	}

	stdinPipe, err := w.cmd.StdinPipe()
	if err != nil {
		return nil, fmt.Errorf("failed to bind stdin: %w", err)
	}
	w.stdin = stdinPipe

	stdoutPipe, err := w.cmd.StdoutPipe()
	if err != nil {
		return nil, fmt.Errorf("failed to bind stdout: %w", err)
	}

	if err := w.cmd.Start(); err != nil {
		return nil, fmt.Errorf("failed to start WinRT worker: %w", err)
	}

	// Monitor worker process exit
	go func() {
		_ = w.cmd.Wait()
		close(w.done)
	}()

	// Wait for confirmation signal from PowerShell
	reader := bufio.NewReader(stdoutPipe)
	readyChan := make(chan error, 1)

	go func() {
		for {
			line, err := reader.ReadString('\n')
			if err != nil {
				readyChan <- fmt.Errorf("Wi-Fi Direct adapter failed to initialize")
				return
			}
			if strings.Contains(line, "HOTSPOT_ONLINE") {
				readyChan <- nil
				return
			}
		}
	}()

	select {
	case err := <-readyChan:
		if err != nil {
			_ = w.Stop()
			return nil, err
		}
	case <-time.After(8 * time.Second):
		_ = w.Stop()
		return nil, fmt.Errorf("timeout waiting for Wi-Fi Direct adapter activation")
	}

	// Resolve the exact network interface created for this AP
	time.Sleep(500 * time.Millisecond) // Allow OS adapter binding
	targets := discovery.GetActiveNetworkTargets()

	var matchedTarget *discovery.NetworkTarget
	for _, t := range targets {
		name := strings.ToLower(t.InterfaceName)
		// Match Microsoft Wi-Fi Direct Virtual Adapter or local SoftAP gateway range
		if strings.Contains(name, "direct") || strings.Contains(name, "wi-fi") || strings.HasPrefix(t.LocalIP.String(), "192.168.137.") {
			matchedTarget = &t
			break
		}
	}

	var localIP, bcastIP net.IP
	ifName := "Wi-Fi Direct"
	if matchedTarget != nil {
		localIP = matchedTarget.LocalIP
		bcastIP = matchedTarget.BroadcastIP
		ifName = matchedTarget.InterfaceName
	} else if len(targets) > 0 {
		localIP = targets[0].LocalIP
		bcastIP = targets[0].BroadcastIP
	} else {
		localIP = net.ParseIP("192.168.137.1")
		bcastIP = net.ParseIP("192.168.137.255")
	}

	return &NetworkInfo{
		SSID:        cfg.SSID,
		Password:    cfg.Password,
		Interface:   ifName,
		LocalIP:     localIP,
		BroadcastIP: bcastIP,
		Band:        Band5GHz,
	}, nil
}

func (w *windowsHotspot) Stop() error {
	w.mu.Lock()
	defer w.mu.Unlock()

	if w.stopped {
		return nil
	}
	w.stopped = true

	// Step 1: Send EOF to trigger clean $pub.Stop() inside PowerShell
	if w.stdin != nil {
		_ = w.stdin.Close()
	}

	// Step 2: Graceful wait with 3-second deadline before forceful kill fallback
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()

	select {
	case <-w.done:
		// PowerShell stopped and exited cleanly
		return nil
	case <-ctx.Done():
		// Emergency fallback if PowerShell hangs
		if w.cmd != nil && w.cmd.Process != nil {
			_ = w.cmd.Process.Kill()
		}
		return fmt.Errorf("hotspot teardown timed out, process killed")
	}
}
