package api

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/Medboy224/medXfer/pkg/engine"
	"github.com/Medboy224/medXfer/pkg/testkit"
)

// WP-B.5: the network benchmark of the developer panel measures round trips and a burst with
// the connected device.
func TestNetworkBenchmarkWithThePeer(t *testing.T) {
	src := filepath.Join(t.TempDir(), "a.bin")
	_ = os.WriteFile(src, make([]byte, 1<<10), 0o644)
	pair := testkit.StartPair(t,
		NewDaemonServerAt(t.TempDir(), 0, t.TempDir(), "Sender"),
		NewDaemonServerAt(t.TempDir(), 0, t.TempDir(), "Receiver"))
	pair.Send([]string{src}, testkit.SendOptions{}) // opens the session
	pair.AcceptOffer(0)
	pair.WaitTransferDone(20 * time.Second)

	// From either end: the phone often runs it on a session the other device opened.
	for _, side := range []struct {
		name string
		node *testkit.Node
	}{{"the device that opened the session", pair.Sender}, {"the other one", pair.Receiver}} {
		id := side.node.Request("test_network", map[string]any{"size_bytes": 1 << 20})
		e := side.node.WaitFor("the benchmark's result", func(e testkit.Event) bool {
			return e.ID == id && (e.Event == "benchmark_network_result" || e.Event == "action_error")
		}, 20*time.Second)
		if e.Event != "benchmark_network_result" {
			t.Fatalf("%s: got %s: %s", side.name, e.Event, e.Data)
		}
		// A result, not an error: the pongs came back. Their time may read 0 here, the clock's
		// steps on Windows being longer than a round trip on this machine.
		var r engine.NetBenchResult
		_ = json.Unmarshal(e.Data, &r)
		if r.ThroughputMBps <= 0 || r.AvgRTTMs < 0 {
			t.Errorf("%s: result %+v; want a throughput", side.name, r)
		}
	}
}
