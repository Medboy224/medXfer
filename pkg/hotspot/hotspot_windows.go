//go:build windows

package hotspot

import (
	"bufio"
	"context"
	"fmt"
	"io"
	"net"
	"os/exec"
	"strconv"
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

	// Embedded WinRT runner
	script := fmt.Sprintf(`
$ErrorActionPreference = 'Stop'
$ssid = '%s'
$pass = '%s'

try {
    # 1. Attempt WinRT Mobile Hotspot with explicit 5 GHz Band
    Add-Type -AssemblyName System.Runtime.WindowsRuntime
    
    # FIXED: Replaced backtick with -match to prevent Go compiler crash
    $asTaskGeneric = [System.WindowsRuntimeSystemExtensions].GetMethods() | ? { $_.Name -eq 'AsTask' -and $_.GetParameters().Count -eq 1 -and $_.GetParameters()[0].ParameterType.Name -match 'IAsyncOperation' }
    
    function Await($WinRtTask, $ResultType) {
        $asTask = $asTaskGeneric.MakeGenericMethod($ResultType)
        $netTask = $asTask.Invoke($null, @($WinRtTask))
        $netTask.Wait(-1) | Out-Null
        $netTask.Result
    }

    [Windows.Networking.NetworkOperators.NetworkOperatorTetheringManager, Windows.Networking.NetworkOperators, ContentType = WindowsRuntime] | Out-Null
    [Windows.Networking.Connectivity.NetworkInformation, Windows.Networking.Connectivity, ContentType = WindowsRuntime] | Out-Null

    $profile = [Windows.Networking.Connectivity.NetworkInformation]::GetInternetConnectionProfile()
    
    if ($profile -ne $null) {
        $tetheringManager = [Windows.Networking.NetworkOperators.NetworkOperatorTetheringManager]::CreateFromConnectionProfile($profile)
        
        if ($tetheringManager.TetheringCapability -eq [Windows.Networking.NetworkOperators.TetheringCapability]::Enabled) {
            $tetheringConfig = $tetheringManager.GetCurrentAccessPointConfiguration()
            $tetheringConfig.Ssid = $ssid
            $tetheringConfig.Passphrase = $pass
            
            # Enforce 5 GHz band in WinRT
            try {
                $tetheringConfig.Band = [Windows.Networking.NetworkOperators.TetheringWiFiBand]::FiveGigahertz
            } catch {}

            Await ($tetheringManager.ConfigureAccessPointAsync($tetheringConfig)) ([Windows.Networking.NetworkOperators.NetworkOperatorTetheringOperationResult]) | Out-Null
            $result = Await ($tetheringManager.StartTetheringAsync()) ([Windows.Networking.NetworkOperators.NetworkOperatorTetheringOperationResult])
            
            if ($result.Status -eq [Windows.Networking.NetworkOperators.TetheringOperationStatus]::Success) {
                Write-Output "HOTSPOT_5GHZ_ONLINE"
                [Console]::In.ReadLine() | Out-Null
                Await ($tetheringManager.StopTetheringAsync()) ([Windows.Networking.NetworkOperators.NetworkOperatorTetheringOperationResult]) | Out-Null
                exit 0
            }
        }
    }
} catch {
    # Proceed to fallback on failure
}

# 2. Fallback: WinRT Wi-Fi Direct Autonomous Group (Runs Offline)
try {
    [Windows.Devices.WiFiDirect.WiFiDirectAdvertisementPublisher, Windows.Devices.WiFiDirect, ContentType = WindowsRuntime] | Out-Null
    $pub = New-Object Windows.Devices.WiFiDirect.WiFiDirectAdvertisementPublisher
    $pub.Advertisement.IsAutonomousGroupOwnerEnabled = $true
    $pub.Advertisement.LegacySettings.IsEnabled = $true
    $pub.Advertisement.LegacySettings.Ssid = $ssid
    $pub.Advertisement.LegacySettings.Passphrase.Password = $pass
    $pub.Start()

    Write-Output "HOTSPOT_P2P_ONLINE"
    [Console]::In.ReadLine() | Out-Null
    $pub.Stop()
} catch {
    Write-Error $_.Exception.Message
    exit 1
}
`, cfg.SSID, cfg.Password)

	w.cmd = exec.Command("powershell", "-NoProfile", "-NonInteractive", "-Command", script)
	w.cmd.SysProcAttr = &syscall.SysProcAttr{
		CreationFlags: 0x08000000, // CREATE_NO_WINDOW (Silent execution)
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
		return nil, fmt.Errorf("failed to start network controller: %w", err)
	}

	go func() {
		_ = w.cmd.Wait()
		close(w.done)
	}()

	reader := bufio.NewReader(stdoutPipe)
	readyChan := make(chan error, 1)

	go func() {
		for {
			line, err := reader.ReadString('\n')
			if err != nil {
				readyChan <- fmt.Errorf("failed to initialize wireless adapter")
				return
			}
			if strings.Contains(line, "HOTSPOT_5GHZ_ONLINE") || strings.Contains(line, "HOTSPOT_P2P_ONLINE") {
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
	case <-time.After(10 * time.Second):
		_ = w.Stop()
		return nil, fmt.Errorf("timeout initializing wireless network")
	}

	// Settle adapter and inspect the actual hardware band/channel
	time.Sleep(1200 * time.Millisecond)
	actualBand, channel := queryActualAdapterBand()

	targets := discovery.GetActiveNetworkTargets()
	var matchedTarget *discovery.NetworkTarget
	for _, t := range targets {
		name := strings.ToLower(t.InterfaceName)
		if strings.Contains(name, "direct") || strings.Contains(name, "wi-fi") || strings.HasPrefix(t.LocalIP.String(), "192.168.137.") {
			matchedTarget = &t
			break
		}
	}

	var localIP, bcastIP net.IP
	ifName := "Wi-Fi Adapter"
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
		Band:        actualBand,
		Channel:     channel,
	}, nil
}

func (w *windowsHotspot) Stop() error {
	w.mu.Lock()
	defer w.mu.Unlock()

	if w.stopped {
		return nil
	}
	w.stopped = true

	if w.stdin != nil {
		_ = w.stdin.Close()
	}

	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()

	select {
	case <-w.done:
		return nil
	case <-ctx.Done():
		if w.cmd != nil && w.cmd.Process != nil {
			_ = w.cmd.Process.Kill()
		}
		return nil
	}
}

func queryActualAdapterBand() (Band, int) {
	out, err := exec.Command("netsh", "wlan", "show", "interfaces").Output()
	if err != nil {
		return Band2GHz, 1
	}

	lines := strings.Split(string(out), "\n")
	channel := 1
	is5GHz := false

	for _, line := range lines {
		lower := strings.ToLower(line)

		// Handles English ("Channel") and French ("Canal") parsing
		if strings.Contains(lower, "canal") || strings.Contains(lower, "channel") {
			parts := strings.Split(line, ":")
			if len(parts) >= 2 {
				if chVal, err := strconv.Atoi(strings.TrimSpace(parts[1])); err == nil {
					channel = chVal
				}
			}
		}
		if strings.Contains(lower, "5 ghz") || strings.Contains(lower, "802.11ac") || strings.Contains(lower, "802.11ax") {
			is5GHz = true
		}
	}

	if channel >= 36 || is5GHz {
		return Band5GHz, channel
	}
	return Band2GHz, channel
}
