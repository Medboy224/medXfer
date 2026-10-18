package api

import (
	"context"
	"os"
	"os/exec"
	"strings"
	"time"
	"unicode"
)

// The device's name (lot « Noms des appareils de confiance », N5): what the other device records
// as its alias once paired, and shows in its scans. It is chosen by the user (xfer name,
// set_config device_name); by default the machine's name, or on Android the phone's maker and
// model, never "localhost" (#136). It travels only after the pairing confirmations (PAIR-07),
// never in an announcement (DISC-06).

// MaxDeviceNameLen bounds a device name, in characters.
const MaxDeviceNameLen = 64

// CleanDeviceName trims name and drops control characters; "" if nothing is left, cut to
// MaxDeviceNameLen characters.
func CleanDeviceName(name string) string {
	name = strings.Map(func(r rune) rune {
		if unicode.IsControl(r) {
			return -1
		}
		return r
	}, name)
	name = strings.TrimSpace(name)
	if r := []rune(name); len(r) > MaxDeviceNameLen {
		name = strings.TrimSpace(string(r[:MaxDeviceNameLen]))
	}
	return name
}

// DefaultDeviceName is the name of a device whose user chose none.
func DefaultDeviceName() string {
	if h := CleanDeviceName(hostname()); h != "" && !strings.EqualFold(h, "localhost") && !strings.HasPrefix(strings.ToLower(h), "localhost.") {
		return h
	}
	if n := androidModel(); n != "" {
		return n
	}
	return "medXfer device"
}

var hostname = func() string { h, _ := os.Hostname(); return h }

// androidModel is the phone's maker and model ("Samsung SM-A576B"), read with getprop: Android
// gives "localhost" as the host name, and the name the user set in its settings is not readable
// from Termux (the app passes it with set_config). "" off Android.
var androidModel = func() string {
	// The system's getprop: Termux puts a script of its own first in PATH, which a static Go
	// binary cannot start ("permission denied").
	getprop := "/system/bin/getprop"
	if _, err := os.Stat(getprop); err != nil {
		if getprop, err = exec.LookPath("getprop"); err != nil {
			return ""
		}
	}
	prop := func(key string) string {
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		out, err := exec.CommandContext(ctx, getprop, key).Output()
		if err != nil {
			return ""
		}
		return CleanDeviceName(string(out))
	}
	for _, key := range []string{"ro.product.marketname", "ro.config.marketing_name", "ro.product.vendor.marketname"} {
		if n := prop(key); n != "" {
			return n
		}
	}
	model, maker := prop("ro.product.model"), prop("ro.product.manufacturer")
	if model == "" {
		return ""
	}
	if maker != "" && !strings.HasPrefix(strings.ToLower(model), strings.ToLower(maker)) {
		return CleanDeviceName(strings.ToUpper(maker[:1]) + maker[1:] + " " + model)
	}
	return model
}

// DeviceName is this device's name: the configured one, or the default.
func DeviceName() string {
	return LoadConfig("", "").DeviceName
}

// SetDeviceName saves name as this device's name.
func SetDeviceName(name string) error {
	cfg := LoadConfig("", "")
	cfg.DeviceName = name
	return SaveConfig(cfg)
}
