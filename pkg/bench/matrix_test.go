package bench

import (
	"context"
	"fmt"
	"slices"
	"testing"
	"time"
)

// DEV-07: a reduced matrix runs each combination through the whole transfer (R6), and
// proposes one of them.
func TestMatrixRunsEachCell(t *testing.T) {
	_, o := ladderPair(t)
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	var seen int
	res, err := RunMatrix(ctx, MatrixOptions{LadderOptions: o, Streams: []int{1, 2}, Chunks: []uint32{1 << 20, 2 << 20},
		OnRun: func(int, int, uint32, float64) { seen++ }})
	if err != nil {
		t.Fatal(err)
	}
	if res.Step != "R6" || len(res.Cells) != 4 || seen != 4 {
		t.Fatalf("step %s, %d cells, %d reported; want R6, 4, 4", res.Step, len(res.Cells), seen)
	}
	for _, c := range res.Cells {
		if c.MBps <= 0 || c.MemoryBytes != Memory(c.Streams, c.ChunkSize) || len(c.Runs) != 1 {
			t.Errorf("cell %+v", c)
		}
	}
	if res.Recommended.MBps <= 0 || res.Current != nil {
		t.Errorf("recommended %+v, current %+v (4 × 4 MiB was not measured)", res.Recommended, res.Current)
	}
	if res.Reliable {
		t.Error("one run per cell was called reliable")
	}
	t.Log("\n" + FormatMatrix(res))
}

func TestMatrixRefusesBadCells(t *testing.T) {
	_, o := ladderPair(t)
	ctx := context.Background()
	for _, m := range []MatrixOptions{
		{LadderOptions: o, Step: "R7"},
		{LadderOptions: o, Streams: []int{9}},
		{LadderOptions: o, Chunks: []uint32{16 << 20}},
	} {
		if _, err := RunMatrix(ctx, m); err == nil {
			t.Errorf("%+v accepted", m)
		}
	}
}

// The rule of the defaults (bible §17.4, D8): within 5 % of the best, the least memory; then
// the faster; then the fewer streams.
func TestRecommend(t *testing.T) {
	cell := func(s int, chunkMiB uint32, mbps float64) MatrixCell {
		return MatrixCell{Streams: s, ChunkSize: chunkMiB << 20, MemoryBytes: Memory(s, chunkMiB<<20), MBps: mbps}
	}
	for _, c := range []struct {
		name  string
		cells []MatrixCell
		want  [2]int // streams, chunk MiB
	}{
		{"the best is also the smallest", []MatrixCell{cell(1, 1, 50), cell(4, 4, 40)}, [2]int{1, 1}},
		{"within 5 % with less memory", []MatrixCell{cell(8, 8, 100), cell(4, 4, 96), cell(2, 4, 94)}, [2]int{4, 4}},
		{"exactly 5 % below still counts", []MatrixCell{cell(8, 8, 100), cell(2, 2, 95)}, [2]int{2, 2}},
		{"same memory: the faster", []MatrixCell{cell(8, 8, 100), cell(2, 4, 97), cell(4, 2, 98)}, [2]int{4, 2}},
		{"same memory and speed: fewer streams", []MatrixCell{cell(8, 8, 100), cell(4, 2, 98), cell(2, 4, 98)}, [2]int{2, 4}},
	} {
		best, rec := Recommend(c.cells, MatrixTolerance)
		if rec.Streams != c.want[0] || rec.ChunkSize != uint32(c.want[1])<<20 {
			t.Errorf("%s: recommended %d × %d MiB; want %d × %d MiB", c.name, rec.Streams, rec.ChunkSize>>20, c.want[0], c.want[1])
		}
		top := 0.0
		for _, cell := range c.cells {
			top = max(top, cell.MBps)
		}
		if best.MBps != top {
			t.Errorf("%s: best %+v; want %.0f MB/s", c.name, best, top)
		}
	}
}

// With several runs, a cell's spread is measured; one above the tolerance makes the
// recommendation unreliable.
func TestMatrixSpread(t *testing.T) {
	_, o := ladderPair(t)
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	res, err := RunMatrix(ctx, MatrixOptions{LadderOptions: o, Step: "R3", Streams: []int{2}, Chunks: []uint32{1 << 20}, Repeat: 3})
	if err != nil {
		t.Fatal(err)
	}
	c := res.Cells[0]
	if len(c.Runs) != 3 || c.Spread < 0 || res.MaxSpread != c.Spread || res.Reliable != (c.Spread <= MatrixTolerance) {
		t.Fatalf("cell %+v, max spread %v, reliable %v", c, res.MaxSpread, res.Reliable)
	}
}

// The runs alternate between the cells, in reverse order every other round: interference at
// one moment hits every cell, not all the runs of one (D8 measures).
func TestMatrixRunsAlternate(t *testing.T) {
	var order []string
	speed := map[string]float64{"1x1": 10, "2x1": 20}
	saved := runMatrixStep
	runMatrixStep = func(_ context.Context, o LadderOptions, step string) (LadderStep, error) {
		key := fmt.Sprintf("%dx%d", o.Streams, o.ChunkSize>>20)
		order = append(order, key)
		return LadderStep{Step: step, MBps: speed[key] + float64(len(order))}, nil
	}
	t.Cleanup(func() { runMatrixStep = saved })
	_, o := ladderPair(t)
	res, err := RunMatrix(context.Background(), MatrixOptions{LadderOptions: o, Streams: []int{1, 2}, Chunks: []uint32{1 << 20}, Repeat: 3})
	if err != nil {
		t.Fatal(err)
	}
	if want := []string{"1x1", "2x1", "2x1", "1x1", "1x1", "2x1"}; !slices.Equal(order, want) {
		t.Fatalf("runs in order %v; want %v", order, want)
	}
	// 1x1 ran 1st, 4th and 5th (10+1, 10+4, 10+5): its median is 14; 2x1: 22, 23, 26: 23.
	if c := res.Cells[0]; c.Streams != 1 || c.MBps != 14 || len(c.Runs) != 3 {
		t.Fatalf("cell %+v", c)
	}
	if c := res.Cells[1]; c.Streams != 2 || c.MBps != 23 {
		t.Fatalf("cell %+v", c)
	}
}

func TestMedian(t *testing.T) {
	for _, c := range []struct {
		in   []float64
		want float64
	}{{[]float64{3}, 3}, {[]float64{5, 1, 3}, 3}, {[]float64{4, 1, 3, 2}, 2.5}} {
		if got := median(c.in); got != c.want {
			t.Errorf("median(%v) = %v; want %v", c.in, got, c.want)
		}
	}
}
