package transfer

// Progress is a snapshot of a transfer, for the interface. Bytes counts each chunk once,
// whatever was sent again: it never goes past Total (#33).
type Progress struct {
	Bytes     int64
	Total     int64
	SpeedMBps float64
	Streams   int // data connections open
	ItemsDone int // items finished: received, skipped or failed
	Items     int
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

// ItemResult is how an item ended.
type ItemResult struct {
	Item      int
	State     ItemState
	SavedPath string
	Err       error
}
