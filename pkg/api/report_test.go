package api

import (
	"os"
	"strings"
	"testing"

	"github.com/Medboy224/medXfer/pkg/diag"
)

// DEV-05: the daemon saves each transfer report, without the file name.
func TestDaemonSavesReportWithoutFileName(t *testing.T) {
	configMu.RLock()
	previous := customConfigDir
	configMu.RUnlock()
	SetCustomConfigDir(t.TempDir())
	t.Cleanup(func() { SetCustomConfigDir(previous) })

	s := NewDaemonServer(0, t.TempDir(), "ReportNode")
	s.StartTransferTracker("Bulletin-salaire-mars.pdf", 1, 4096)
	s.RecordTransferRetry()
	rep := s.FinishTransferTracker()
	if rep == nil || rep.Retries != 1 {
		t.Fatalf("FinishTransferTracker = %+v; want a report with 1 retry", rep)
	}

	files, err := diag.ListReports(ReportsDir())
	if err != nil || len(files) != 1 {
		t.Fatalf("saved reports = %v, %v; want exactly one", files, err)
	}
	data, _ := os.ReadFile(files[0])
	if strings.Contains(string(data), "Bulletin") {
		t.Fatalf("saved report contains the file name:\n%s", data)
	}
	if strings.Contains(rep.FormattedReport, "Bulletin") {
		t.Fatalf("dashboard text contains the file name:\n%s", rep.FormattedReport)
	}
}
