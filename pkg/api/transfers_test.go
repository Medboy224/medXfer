package api

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/Medboy224/medXfer/pkg/testkit"
)

func folderOf(t *testing.T, n int) (string, string) {
	t.Helper()
	sendDir := t.TempDir()
	folder := filepath.Join(sendDir, "shared")
	_ = os.MkdirAll(folder, 0o755)
	for i := 0; i < n; i++ {
		_ = os.WriteFile(filepath.Join(folder, fmt.Sprintf("file_%d.txt", i)), []byte(fmt.Sprintf("content %d", i)), 0o644)
	}
	return sendDir, folder
}

// LIM-08: one transfer at a time with the paired device. A busy sender makes no offer; a
// busy receiver refuses one, and the sender's dashboard says why.
func TestOneTransferAtATime(t *testing.T) {
	_, folder := folderOf(t, 2)

	sender := NewDaemonServer(0, t.TempDir(), "SenderNode")
	pair := testkit.StartPair(t, sender, NewDaemonServer(0, t.TempDir(), "ReceiverNode"))
	sender.mu.Lock()
	sender.transferCancel = func() {} // a transfer in progress
	sender.mu.Unlock()
	pair.Send([]string{folder}, testkit.SendOptions{})
	e := pair.Sender.WaitEvent("action_error", 0)
	var data struct{ Error string }
	_ = e.Decode(&data)
	if !strings.Contains(data.Error, "already in progress") {
		t.Fatalf("action_error %q; want a transfer already in progress", data.Error)
	}
	pair.Receiver.ExpectNoEvent("incoming_offer", 500*time.Millisecond)

	receiver := NewDaemonServer(0, t.TempDir(), "BusyReceiver")
	pair = testkit.StartPair(t, NewDaemonServer(0, t.TempDir(), "SenderNode2"), receiver)
	receiver.mu.Lock()
	receiver.transferCancel = func() {}
	receiver.mu.Unlock()
	pair.Send([]string{folder}, testkit.SendOptions{})
	e = pair.Sender.WaitEvent("transfer_rejected", 0)
	var rej struct{ Message string }
	_ = e.Decode(&rej)
	if !strings.Contains(rej.Message, "busy") {
		t.Fatalf("transfer_rejected %q; want the peer busy", rej.Message)
	}
	pair.Receiver.ExpectNoEvent("incoming_offer", 300*time.Millisecond)
}

// D6: the sender's skip_file reaches the receiver, whose scheduler leaves the item out.
func TestSenderSkipReachesTheReceiver(t *testing.T) {
	_, folder := folderOf(t, 3)
	recvDir := t.TempDir()
	pair := testkit.StartPair(t, NewDaemonServer(0, t.TempDir(), "SenderNode"), NewDaemonServer(0, recvDir, "ReceiverNode"))

	pair.Send([]string{folder}, testkit.SendOptions{})
	pair.Receiver.WaitEvent("incoming_offer", 0)
	pair.Sender.Request("skip_file", `{"item_index": 1}`)
	pair.Receiver.WaitEvent("file_skipped", 0)
	pair.Receiver.Request("respond_offer", map[string]any{"accept": true})
	pair.WaitTransferDone(0)

	for i, want := range []bool{true, false, true} {
		_, err := os.Stat(filepath.Join(recvDir, "shared", fmt.Sprintf("file_%d.txt", i)))
		if (err == nil) != want {
			t.Fatalf("file_%d.txt received = %v; want %v", i, err == nil, want)
		}
	}
}

// DEV-03, DEV-04: the report sent with transfer_complete carries the stages of the
// receiver's side and names the limiting one.
func TestTransferReportHasStages(t *testing.T) {
	_, folder := folderOf(t, 3)
	pair := testkit.StartPair(t, NewDaemonServer(0, t.TempDir(), "SenderNode"), NewDaemonServer(0, t.TempDir(), "ReceiverNode"))
	pair.Send([]string{folder}, testkit.SendOptions{})
	pair.AcceptOffer(0)
	done := pair.WaitTransferDone(0)
	var data struct {
		Report struct {
			SchemaVersion int                        `json:"schema_version"`
			Stages        map[string]json.RawMessage `json:"stages"`
			Bottleneck    *struct{ Stage string }    `json:"bottleneck"`
		} `json:"summary_report"`
	}
	if err := done.Decode(&data); err != nil {
		t.Fatal(err)
	}
	r := data.Report
	if r.SchemaVersion < 2 || r.Bottleneck == nil || r.Bottleneck.Stage == "" {
		t.Fatalf("report without a limiting stage: %+v", r)
	}
	for _, stage := range []string{"net_wait", "hash", "disk_write"} {
		if _, ok := r.Stages[stage]; !ok {
			t.Errorf("stage %s missing from the report (%v)", stage, r.Stages)
		}
	}
}
