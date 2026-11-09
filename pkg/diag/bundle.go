package diag

import (
	"archive/zip"
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"time"
)

// BundleOptions says where the diagnostic bundle takes its content.
type BundleOptions struct {
	ReportsDir string // transfer reports (already free of names and addresses, DEV-05)
	JournalDir string // journal.log and journal.log.1
	Config     any    // configuration to include, already stripped of personal data
	MaxReports int    // newest reports to include; 0 means 10
	// AppLog is the application's own log (its Bluetooth, its networks), which the core does not
	// see: its last MaxAppLog bytes, masked as the rest.
	AppLog string
}

// MaxAppLog bounds the application's log in the bundle.
const MaxAppLog = 512 << 10

// BundleManifest describes the bundle and the device that produced it.
type BundleManifest struct {
	SchemaVersion int       `json:"schema_version"`
	GeneratedAt   time.Time `json:"generated_at"`
	AppVersion    string    `json:"app_version"`
	OS            string    `json:"os"`
	Arch          string    `json:"arch"`
	GoVersion     string    `json:"go_version"`
	NumCPU        int       `json:"num_cpu"`
	Files         []string  `json:"files"`
}

// WriteBundle writes the diagnostic bundle (DEV-11) as a zip archive: manifest, configuration,
// newest reports, journal, the application's log. Every text is masked again on the way out, with Secrets: the
// bundle never contains keys, PINs, tokens, paths or user names, whatever the files hold.
func WriteBundle(w io.Writer, opts BundleOptions) error {
	if opts.MaxReports <= 0 {
		opts.MaxReports = 10
	}
	zw := zip.NewWriter(w)
	m := BundleManifest{
		SchemaVersion: SchemaVersion,
		GeneratedAt:   time.Now().UTC(),
		AppVersion:    AppVersion,
		OS:            runtime.GOOS,
		Arch:          runtime.GOARCH,
		GoVersion:     runtime.Version(),
		NumCPU:        runtime.NumCPU(),
	}
	add := func(name string, data []byte) error {
		fw, err := zw.Create(name)
		if err != nil {
			return err
		}
		_, err = fw.Write([]byte(Secrets.Redact(string(data))))
		if err == nil {
			m.Files = append(m.Files, name)
		}
		return err
	}

	if opts.Config != nil {
		data, err := json.MarshalIndent(opts.Config, "", "  ")
		if err != nil {
			return err
		}
		if err := add("config.json", data); err != nil {
			return err
		}
	}
	if opts.ReportsDir != "" {
		reports, _ := ListReports(opts.ReportsDir)
		if len(reports) > opts.MaxReports {
			reports = reports[len(reports)-opts.MaxReports:]
		}
		for _, p := range reports {
			data, err := os.ReadFile(p)
			if err != nil {
				continue
			}
			if err := add("reports/"+filepath.Base(p), data); err != nil {
				return err
			}
		}
	}
	if opts.JournalDir != "" {
		for _, p := range JournalFiles(opts.JournalDir) {
			data, err := os.ReadFile(p)
			if err != nil {
				continue
			}
			if err := add("journal/"+filepath.Base(p), data); err != nil {
				return err
			}
		}
	}

	if log := opts.AppLog; log != "" {
		if len(log) > MaxAppLog {
			log = log[len(log)-MaxAppLog:]
			if i := strings.IndexByte(log, '\n'); i >= 0 {
				log = log[i+1:] // from a whole line
			}
		}
		if err := add("app.log", []byte(log)); err != nil {
			return err
		}
	}

	data, err := json.MarshalIndent(m, "", "  ")
	if err != nil {
		return err
	}
	fw, err := zw.Create("manifest.json")
	if err != nil {
		return err
	}
	if _, err := fw.Write(data); err != nil {
		return err
	}
	return zw.Close()
}
