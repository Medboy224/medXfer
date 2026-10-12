package main

// The CLI side of the transfer protocol v1 (bible §8.3, WP-4.3): one offer for a file, several
// files or a folder, and the progress shown in the terminal.

import (
	"context"
	"fmt"
	"sync"

	"github.com/Medboy224/medXfer/pkg/protocol"
	"github.com/Medboy224/medXfer/pkg/transfer"
	"github.com/Medboy224/medXfer/pkg/ui"
)

// offerSummary is how the terminal names an offer: a file, or a folder with its count.
func offerSummary(o protocol.Offer) string {
	if len(o.Items) == 1 {
		return fmt.Sprintf("%s (%.2f MB)", o.Items[0].Path, float64(o.TotalBytes)/(1<<20))
	}
	return fmt.Sprintf("'%s' (%d files, %.2f MB)", o.Root, len(o.Items), float64(o.TotalBytes)/(1<<20))
}

// cliEvents shows a transfer in the terminal: one progress bar for the whole offer, and the
// items that fail or are skipped.
type cliEvents struct {
	mu        sync.Mutex
	bar       *ui.ProgressBar
	sender    bool
	cancel    context.CancelFunc // sender: called once the receiver is done
	sawStream bool
	finished  bool
}

func newCLIEvents(sender bool, cancel context.CancelFunc) *cliEvents {
	return &cliEvents{bar: ui.NewProgressBar(), sender: sender, cancel: cancel}
}

func (e *cliEvents) OnProgress(p transfer.Progress) {
	e.mu.Lock()
	defer e.mu.Unlock()
	if e.bar != nil {
		e.bar.Render(p.Bytes, p.Total, p.SpeedMBps)
	}
	if p.Streams > 0 {
		e.sawStream = true
	}
	// The sender is done once every chunk was served AND the receiver, which closes its
	// connections when it has written everything, has gone (#44).
	if e.sender && !e.finished && e.sawStream && p.Bytes >= p.Total && p.Streams == 0 {
		e.finished = true
		e.finish()
		fmt.Println("\n[+] Transfer completed.")
		if e.cancel != nil {
			e.cancel()
		}
	}
}

func (e *cliEvents) OnItem(ev transfer.ItemEvent) {
	switch ev.State {
	case transfer.ItemFailed:
		fmt.Printf("\n[-] '%s' failed: %v\n", ev.Path, ev.Err)
	case transfer.ItemSkipped:
		fmt.Printf("\n[*] '%s' skipped.\n", ev.Path)
	}
}

// finish ends the progress bar. Called with e.mu held.
func (e *cliEvents) finish() {
	if e.bar != nil {
		e.bar.Finish()
		e.bar = nil
	}
}

// done ends the display of a receiver's transfer.
func (e *cliEvents) done() {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.finish()
}
