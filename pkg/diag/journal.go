package diag

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

// Journal is the daemon's diagnostic log (DEV-11): masked text, appended to journal.log in
// its folder. When the file passes half of its budget it becomes journal.log.1, so the two
// files together never exceed the budget (a ring over files: the daemon may crash, and
// `xfer bundle` runs in another process).
type Journal struct {
	mu       sync.Mutex
	dir      string
	f        *os.File
	size     int64
	half     int64
	redactor *Redactor
	partial  []byte // a line not yet terminated by '\n'
}

// JournalBudget is the default size of the journal: 2 MiB over two files.
const JournalBudget = 2 << 20

const journalName = "journal.log"

// OpenJournal opens (or continues) the journal in dir. A nil redactor means Secrets.
func OpenJournal(dir string, budget int64, redactor *Redactor) (*Journal, error) {
	if budget < 2*1024 {
		budget = 2 * 1024
	}
	if redactor == nil {
		redactor = Secrets
	}
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return nil, err
	}
	j := &Journal{dir: dir, half: budget / 2, redactor: redactor}
	if err := j.open(); err != nil {
		return nil, err
	}
	return j, nil
}

func (j *Journal) open() error {
	f, err := os.OpenFile(filepath.Join(j.dir, journalName), os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o600)
	if err != nil {
		return err
	}
	st, err := f.Stat()
	if err != nil {
		f.Close()
		return err
	}
	j.f, j.size = f, st.Size()
	return nil
}

// Write masks p line by line and appends it. It implements io.Writer, so it can receive the
// output of the log package. A line split across writes is masked once complete.
func (j *Journal) Write(p []byte) (int, error) {
	j.mu.Lock()
	defer j.mu.Unlock()
	j.partial = append(j.partial, p...)
	i := strings.LastIndexByte(string(j.partial), '\n')
	if i < 0 {
		if len(j.partial) > 64*1024 { // no newline for too long: flush anyway
			i = len(j.partial) - 1
		} else {
			return len(p), nil
		}
	}
	text := string(j.partial[:i+1])
	j.partial = append(j.partial[:0], j.partial[i+1:]...)
	return len(p), j.append(j.redactor.Redact(text))
}

// Event records that the daemon emitted the event name, without its data: the data of events
// carry file names, PINs and tokens.
func (j *Journal) Event(name string) { j.Printf("event %s", name) }

// Printf records a timestamped line.
func (j *Journal) Printf(format string, args ...any) {
	line := time.Now().Format("2006/01/02 15:04:05.000 ") + fmt.Sprintf(format, args...)
	if !strings.HasSuffix(line, "\n") {
		line += "\n"
	}
	_, _ = j.Write([]byte(line))
}

func (j *Journal) append(text string) error {
	if j.f == nil {
		return os.ErrClosed
	}
	if j.size+int64(len(text)) > j.half && j.size > 0 {
		_ = j.f.Close()
		cur := filepath.Join(j.dir, journalName)
		if err := os.Rename(cur, cur+".1"); err != nil {
			_ = os.Remove(cur + ".1") // Windows: Rename does not replace an existing file
			_ = os.Rename(cur, cur+".1")
		}
		if err := j.open(); err != nil {
			j.f = nil
			return err
		}
	}
	if int64(len(text)) > j.half { // a single huge write keeps only its end
		text = text[len(text)-int(j.half):]
	}
	n, err := j.f.WriteString(text)
	j.size += int64(n)
	return err
}

// Close flushes a pending partial line and closes the file.
func (j *Journal) Close() error {
	j.mu.Lock()
	defer j.mu.Unlock()
	if len(j.partial) > 0 {
		_ = j.append(j.redactor.Redact(string(j.partial) + "\n"))
		j.partial = nil
	}
	if j.f == nil {
		return nil
	}
	err := j.f.Close()
	j.f = nil
	return err
}

// JournalFiles returns the journal files of dir, oldest first.
func JournalFiles(dir string) []string {
	var files []string
	for _, name := range []string{journalName + ".1", journalName} {
		p := filepath.Join(dir, name)
		if _, err := os.Stat(p); err == nil {
			files = append(files, p)
		}
	}
	return files
}
