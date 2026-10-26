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
	rep := s.FinishTransferTracker(diag.OutcomeCompleted)
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

// WP-B.5: after a restart, the last report is the newest one saved, for the developer panel.
func TestLastReportSurvivesARestart(t *testing.T) {
	configMu.RLock()
	previous := customConfigDir
	configMu.RUnlock()
	SetCustomConfigDir(t.TempDir())
	t.Cleanup(func() { SetCustomConfigDir(previous) })

	if rep := NewDaemonServer(0, t.TempDir(), "Fresh").GetLastSummaryReport(); rep != nil {
		t.Fatalf("no report saved yet, got %+v", rep)
	}
	s := NewDaemonServer(0, t.TempDir(), "Before")
	s.StartTransferTracker("a.bin", 1, 4096)
	s.RecordTransferRetry()
	s.RecordTransferRetry()
	s.FinishTransferTracker(diag.OutcomeCompleted)

	rep := NewDaemonServer(0, t.TempDir(), "After").GetLastSummaryReport()
	if rep == nil || rep.Retries != 2 || rep.FormattedReport == "" {
		t.Fatalf("after a restart: %+v; want the saved report, 2 retries, with its text", rep)
	}
}
