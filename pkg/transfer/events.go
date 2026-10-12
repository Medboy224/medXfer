package transfer

import "time"

// Progress is a snapshot of a transfer, for the interface. Bytes counts each chunk once,
// whatever was sent again: it never goes past Total (#33).
type Progress struct {
	Bytes     int64
	Total     int64
	SpeedMBps float64
	Streams   int // data connections open
	ItemsDone int // items finished: received, skipped or failed
	Items     int
	// Sender: the receiver said every item ended (DONE). After a resume, a skip or a file
	// already on the receiver, the sender serves less than Total: only this tells it the
	// transfer is over (#33). Partial: some items were skipped or failed.
	Finished bool
	Partial  bool
}

// ItemState is where an item of an offer stands.
type ItemState int

const (
	ItemPending ItemState = iota
	ItemStarted           // its file is open on the receiver
	ItemDone              // received, or already complete on disk
	ItemSkipped           // skipped by a user
	ItemFailed
)

func (s ItemState) String() string {
	return [...]string{"pending", "started", "done", "skipped", "failed"}[s]
}

// ItemEvent tells that an item changed state.
type ItemEvent struct {
	Item      int
	Path      string // as offered
	Size      int64
	State     ItemState
	SavedPath string // receiver, ItemDone: where the file is
	Err       error  // ItemFailed
}

// Events receives the events of a transfer. Calls may come from several goroutines, never
// at the same time for the same transfer.
type Events interface {
	OnProgress(Progress)
	OnItem(ItemEvent)
}

// Stages of a chunk, measured for diag (DEV-03).
const (
	// Receiver.
	StageNetWait   = "net_wait"   // waiting for a CHUNK, from the moment the worker can take it
	StageHash      = "hash"       // SHA-256 of a chunk (both sides)
	StageDiskWrite = "disk_write" // writing a verified chunk
	StageFsync     = "fsync"      // a group commit: data synced, then the state
	// Sender.
	StageDiskRead = "disk_read" // reading a chunk from the source
	StageNetWrite = "net_write" // writing a CHUNK to the connection
)

// StageEvents is implemented by Events that also want the time of each stage of each chunk
// (DEV-03); pkg/diag aggregates them and never decides anything (DEV-01). parallel is how
// many of these stages may run at once. Unlike the other events, OnStage is called from
// several goroutines at the same time.
type StageEvents interface {
	OnStage(stage string, parallel int, d time.Duration, bytes int64)
}

// ItemResult is how an item ended.
type ItemResult struct {
	Item      int
	State     ItemState
	SavedPath string
	Err       error
}
