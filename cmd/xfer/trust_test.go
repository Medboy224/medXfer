package main

import (
	"bytes"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Medboy224/medXfer/pkg/identity"
)

func TestIDCommand(t *testing.T) {
	dir := t.TempDir()
	var out bytes.Buffer
	if err := runID(&out, dir); err != nil {
		t.Fatal(err)
	}
	id, err := identity.LoadOrCreate(dir)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.String(), id.Fingerprint.Short()) || !strings.Contains(out.String(), id.Fingerprint.String()) {
		t.Fatalf("xfer id does not show the stored identity:\n%s", out.String())
	}
}

func TestTrustCommand(t *testing.T) {
	dir := t.TempDir()
	peer, err := identity.LoadOrCreate(filepath.Join(dir, "peer"))
	if err != nil {
		t.Fatal(err)
	}
	ts, err := identity.OpenTrustStore(filepath.Join(dir, identity.TrustFile))
	if err != nil {
		t.Fatal(err)
	}
	if err := ts.Add(identity.Peer{Fingerprint: peer.Fingerprint, Alias: "phone"}); err != nil {
		t.Fatal(err)
	}

	var out bytes.Buffer
	if err := runTrust(&out, dir, []string{"list"}); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.String(), peer.Fingerprint.Short()) || !strings.Contains(out.String(), "phone") {
		t.Fatalf("trust list:\n%s", out.String())
	}

	out.Reset()
	if err := runTrust(&out, dir, []string{"forget", peer.Fingerprint.Short()}); err != nil {
		t.Fatal(err)
	}
	ts2, _ := identity.OpenTrustStore(filepath.Join(dir, identity.TrustFile))
	if ts2.Contains(peer.Fingerprint) {
		t.Fatal("xfer trust forget did not remove the device")
	}
	if err := runTrust(&out, dir, []string{"forget", peer.Fingerprint.Short()}); err == nil {
		t.Fatal("forgetting an unknown device succeeded")
	}
	if err := runTrust(&out, dir, []string{"bogus"}); err == nil {
		t.Fatal("an unknown subcommand succeeded")
	}
}
