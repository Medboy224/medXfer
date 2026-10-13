package bench

// The bench matrix (bible §17.4, DEV-07): one step of the ladder, measured for each number of
// streams and each chunk size. The defaults it proposes are the combination within 5 % of the
// best throughput that needs the least memory: a receiver holds Streams × 2 chunks (bible
// §8.3). Decision D8 of the J4b plan: the defaults change only if the matrix proposes another
// combination between the devices that matter.

import (
	"context"
	"fmt"
	"slices"
	"strings"

	"github.com/Medboy224/medXfer/pkg/diag"
	"github.com/Medboy224/medXfer/pkg/protocol"
	"github.com/Medboy224/medXfer/pkg/transfer"
)

// Matrix defaults (bible §17.4).
var (
	MatrixStreams = []int{1, 2, 4, 8}
	MatrixChunks  = []uint32{1 << 20, 2 << 20, 4 << 20, 8 << 20}
)

// MatrixTolerance: a combination within 5 % of the best is as good (a parameter, rule R4).
const MatrixTolerance = 0.05

// MatrixOptions are the settings of a matrix: the ladder's, whose Streams and ChunkSize each
// cell replaces.
type MatrixOptions struct {
	LadderOptions
	Step    string   // the step measured, R1 to R6 (default R6: the whole transfer)
	Streams []int    // default MatrixStreams
	Chunks  []uint32 // default MatrixChunks
	Repeat  int      // runs per cell, whose median counts (default 1)
	// OnRun, when set, is called after each run: a long matrix shows its progress.
	OnRun func(round, streams int, chunk uint32, mbps float64)
}

// MatrixCell is the measure of one combination.
type MatrixCell struct {
	Streams     int              `json:"streams"`
	ChunkSize   uint32           `json:"chunk_size"`
	MemoryBytes int64            `json:"memory_bytes"` // Streams × 2 × ChunkSize
	MBps        float64          `json:"mbps"`         // median of the runs
	Runs        []float64        `json:"runs_mbps"`
	Spread      float64          `json:"spread"`               // (max - min) / median of the runs; 0 with one run
	Bottleneck  *diag.Bottleneck `json:"bottleneck,omitempty"` // of the last run
}

// MatrixResult is a whole matrix, saved in bench-results/ and printed by --json.
type MatrixResult struct {
	SchemaVersion int          `json:"schema_version"`
	Step          string       `json:"step"`
	Layer         string       `json:"layer"`
	Bytes         int64        `json:"bytes"` // per run
	Repeat        int          `json:"repeat"`
	Tolerance     float64      `json:"tolerance"`
	Cells         []MatrixCell `json:"cells"`
	Best          MatrixCell   `json:"best"`
	Recommended   MatrixCell   `json:"recommended"`
	// Current: the cell of the defaults in use (DefaultWorkers × DefaultChunkSize), if
	// measured; KeepDefaults: the recommendation is that cell (D8).
	Current      *MatrixCell `json:"current,omitempty"`
	KeepDefaults bool        `json:"keep_defaults"`
	// MaxSpread is the largest spread of a cell. Above the tolerance, or with one run per
	// cell, the measures do not tell combinations apart: the recommendation is within the
	// noise (Reliable false), and the defaults stay (D8).
	MaxSpread float64 `json:"max_spread"`
	Reliable  bool    `json:"reliable"`
	// Who measured: the receiving side, filled by the caller.
	Host string `json:"host,omitempty"`
	OS   string `json:"os,omitempty"`
}

// Memory is what a receiver holds for streams connections of chunk-sized buffers, two each.
func Memory(streams int, chunk uint32) int64 { return int64(streams) * 2 * int64(chunk) }

// runMatrixStep measures one run of a cell; tests replace it to check the order of the runs.
var runMatrixStep = func(ctx context.Context, o LadderOptions, step string) (LadderStep, error) {
	return o.runStep(ctx, step)
}

// RunMatrix measures o.Step for each combination of o.Streams × o.Chunks. The runs alternate:
// each round measures every cell once, in reverse order every other round. A moment of
// interference on a Wi-Fi link then hits every cell alike, instead of all the runs of one
// (the measures of D8, bench-results/README.md).
func RunMatrix(ctx context.Context, o MatrixOptions) (*MatrixResult, error) {
	if o.Step == "" {
		o.Step = "R6"
	}
	if len(o.Streams) == 0 {
		o.Streams = MatrixStreams
	}
	if len(o.Chunks) == 0 {
		o.Chunks = MatrixChunks
	}
	if o.Repeat < 1 {
		o.Repeat = 1
	}
	res := &MatrixResult{SchemaVersion: diag.SchemaVersion, Step: o.Step, Bytes: o.Size, Repeat: o.Repeat,
		Tolerance: MatrixTolerance}
	for _, s := range Steps {
		if s.Name == o.Step {
			res.Layer = s.Layer
		}
	}
	if res.Layer == "" {
		return nil, fmt.Errorf("bench matrix: unknown step %q", o.Step)
	}
	type plan struct {
		lo   LadderOptions
		cell MatrixCell
	}
	var plans []plan
	for _, chunk := range o.Chunks {
		if chunk < protocol.MinChunkSize || chunk > protocol.MaxChunkSize {
			return nil, fmt.Errorf("bench matrix: chunk size %d outside [%d, %d]", chunk, protocol.MinChunkSize, protocol.MaxChunkSize)
		}
		for _, streams := range o.Streams {
			if streams < 1 || streams > transfer.MaxWorkers {
				return nil, fmt.Errorf("bench matrix: %d streams outside [1, %d]", streams, transfer.MaxWorkers)
			}
			lo := o.LadderOptions
			lo.Streams, lo.ChunkSize = streams, chunk
			lo, err := lo.withDefaults()
			if err != nil {
				return nil, err
			}
			plans = append(plans, plan{lo, MatrixCell{Streams: streams, ChunkSize: chunk, MemoryBytes: Memory(streams, chunk)}})
		}
	}
	for round := 0; round < o.Repeat; round++ {
		for k := range plans {
			p := &plans[k]
			if round%2 == 1 {
				p = &plans[len(plans)-1-k]
			}
			step, err := runMatrixStep(ctx, p.lo, o.Step)
			if err != nil {
				return res, fmt.Errorf("%d streams × %s: %w", p.cell.Streams, diag.FormatBytes(int64(p.cell.ChunkSize)), err)
			}
			p.cell.Runs = append(p.cell.Runs, step.MBps)
			p.cell.Bottleneck = step.Bottleneck
			if o.OnRun != nil {
				o.OnRun(round+1, p.cell.Streams, p.cell.ChunkSize, step.MBps)
			}
		}
	}
	for _, p := range plans {
		cell := p.cell
		cell.MBps = median(cell.Runs)
		if cell.MBps > 0 {
			cell.Spread = round2((slices.Max(cell.Runs) - slices.Min(cell.Runs)) / cell.MBps)
		}
		res.MaxSpread = max(res.MaxSpread, cell.Spread)
		res.Cells = append(res.Cells, cell)
	}
	o.quit(ctx)
	res.Best, res.Recommended = Recommend(res.Cells, MatrixTolerance)
	res.Reliable = o.Repeat > 1 && res.MaxSpread <= MatrixTolerance
	for i, c := range res.Cells {
		if c.Streams == transfer.DefaultWorkers && c.ChunkSize == protocol.DefaultChunkSize {
			res.Current = &res.Cells[i]
			res.KeepDefaults = c.Streams == res.Recommended.Streams && c.ChunkSize == res.Recommended.ChunkSize
		}
	}
	return res, nil
}

// Recommend returns the best cell, and the one to use as defaults: within tolerance of the
// best throughput, the least memory; then the faster; then the fewer streams.
func Recommend(cells []MatrixCell, tolerance float64) (best, rec MatrixCell) {
	if len(cells) == 0 {
		return
	}
	for _, c := range cells {
		if c.MBps > best.MBps {
			best = c
		}
	}
	first := true
	for _, c := range cells {
		if c.MBps < (1-tolerance)*best.MBps {
			continue
		}
		better := first || c.MemoryBytes < rec.MemoryBytes ||
			(c.MemoryBytes == rec.MemoryBytes && (c.MBps > rec.MBps || (c.MBps == rec.MBps && c.Streams < rec.Streams)))
		if better {
			rec, first = c, false
		}
	}
	return best, rec
}

func median(v []float64) float64 {
	s := slices.Clone(v)
	slices.Sort(s)
	n := len(s)
	if n == 0 {
		return 0
	}
	if n%2 == 1 {
		return s[n/2]
	}
	return round2((s[n/2-1] + s[n/2]) / 2)
}

// FormatMatrix renders a matrix: throughput by streams (rows) and chunk size (columns), then
// the recommendation.
func FormatMatrix(r *MatrixResult) string {
	var sb strings.Builder
	fmt.Fprintf(&sb, "Bench matrix: step %s (%s), %s per run, %d run(s) per cell, MB/s\n",
		r.Step, r.Layer, diag.FormatBytes(r.Bytes), r.Repeat)
	var streams []int
	var chunks []uint32
	for _, c := range r.Cells {
		if !slices.Contains(streams, c.Streams) {
			streams = append(streams, c.Streams)
		}
		if !slices.Contains(chunks, c.ChunkSize) {
			chunks = append(chunks, c.ChunkSize)
		}
	}
	slices.Sort(streams)
	slices.Sort(chunks)
	sb.WriteString("  streams \\ chunk")
	for _, ch := range chunks {
		fmt.Fprintf(&sb, " %9s", diag.FormatBytes(int64(ch)))
	}
	sb.WriteString("\n")
	for _, s := range streams {
		fmt.Fprintf(&sb, "  %15d", s)
		for _, ch := range chunks {
			cell := "-"
			for _, c := range r.Cells {
				if c.Streams == s && c.ChunkSize == ch {
					mark := " "
					if c.Streams == r.Recommended.Streams && c.ChunkSize == r.Recommended.ChunkSize {
						mark = "*"
					}
					cell = fmt.Sprintf("%.1f%s", c.MBps, mark)
				}
			}
			fmt.Fprintf(&sb, " %9s", cell)
		}
		sb.WriteString("\n")
	}
	fmt.Fprintf(&sb, "Best: %d streams × %s, %.1f MB/s\n", r.Best.Streams, diag.FormatBytes(int64(r.Best.ChunkSize)), r.Best.MBps)
	fmt.Fprintf(&sb, "Recommended (*): %d streams × %s, %.1f MB/s, %s of buffers: within %.0f %% of the best, least memory\n",
		r.Recommended.Streams, diag.FormatBytes(int64(r.Recommended.ChunkSize)), r.Recommended.MBps,
		diag.FormatBytes(r.Recommended.MemoryBytes), r.Tolerance*100)
	if !r.Reliable {
		if r.Repeat < 2 {
			sb.WriteString("Not reliable: one run per cell says nothing of the noise (use --repeat 3).\n")
		} else {
			fmt.Fprintf(&sb, "Not reliable: the runs of a cell differ by up to %.0f %%, more than the %.0f %% tolerance:\n"+
				"the combinations are within the noise of this link.\n", r.MaxSpread*100, r.Tolerance*100)
		}
	}
	if r.Current != nil {
		verdict := "keep them"
		if !r.KeepDefaults && r.Reliable {
			verdict = "the recommendation differs"
		} else if !r.KeepDefaults {
			verdict = "keep them: the recommendation is not reliable"
		}
		fmt.Fprintf(&sb, "Current defaults: %d streams × %s, %.1f MB/s: %s\n", r.Current.Streams,
			diag.FormatBytes(int64(r.Current.ChunkSize)), r.Current.MBps, verdict)
	}
	return sb.String()
}
