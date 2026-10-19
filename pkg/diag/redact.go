package diag

import (
	"os"
	"os/user"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"sync"
	"unicode"
	"unicode/utf8"
)

// Redactor masks secrets and personal data in diagnostic text (bible chapter 13, DEV-11):
// the known secrets of this process (PIN, tokens, pairing code), then paths, user and host
// names, then generic patterns that catch secrets nobody registered.
type Redactor struct {
	mu      sync.RWMutex
	secrets map[string]string // value -> label
	order   []string          // secrets, longest first
}

// Secrets is the process-wide redactor used by the journal and the diagnostic bundle. Code
// that creates a secret registers it here at once.
var Secrets = NewRedactor()

const maxRedactorSecrets = 256 // rotated PINs accumulate; keep the newest

// NewRedactor returns a redactor with no registered secret.
func NewRedactor() *Redactor { return &Redactor{secrets: map[string]string{}} }

// Add registers secret values to mask as [label]. Values shorter than 4 characters are
// ignored: masking them would garble ordinary text.
func (r *Redactor) Add(label string, values ...string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	for _, v := range values {
		if len(v) < 4 {
			continue
		}
		if _, ok := r.secrets[v]; !ok {
			r.order = append(r.order, v)
		}
		r.secrets[v] = label
	}
	for len(r.order) > maxRedactorSecrets {
		delete(r.secrets, r.order[0])
		r.order = r.order[1:]
	}
}

var (
	// Paths reveal user names and file names: masked whole (chapter 13: paths only at debug level).
	// A Windows path: its folders may hold spaces (a separator ends them), its last part stops
	// at a space or a punctuation mark, so the rest of the line stays (audit 13, #156). A quoted
	// path is masked up to its quote: there, the file name may hold spaces too.
	reWinQuoted = regexp.MustCompile(`(?i)"[a-z]:[\\/][^"\r\n]*"|'[a-z]:[\\/][^'\r\n]*'`)
	reWinPath   = regexp.MustCompile(`(?i)\b[a-z]:(?:\\(?:[^\\/"'\r\n<>|:*?]*\\)*|/(?:[^\\/"'\r\n<>|:*?]*/)*)[^\\/\s"'<>|:*?,;]*`)
	reUnixPath  = regexp.MustCompile(`(?:/(?:home|Users|root|data|sdcard|storage|mnt|media|tmp|var|private)\b)[^\s"'<>|]*`)
	reAuth      = regexp.MustCompile(`(?i)(authorization:?\s*)(bearer\s+)?\S+`)
	reTokenKV   = regexp.MustCompile(`(?i)\b(token|pin|code|secret|password|key)(["']?\s*[=:]\s*["']?)[^\s"'&,;]+`)
	reSubproto  = regexp.MustCompile(`token\.[0-9a-fA-F]+`)
	reLongHex   = regexp.MustCompile(`\b[0-9a-fA-F]{24,}\b`)
	rePairing   = regexp.MustCompile(`\b\d{3}-\d{3}\b`)
)

// Redact returns s with every secret and personal datum masked.
func (r *Redactor) Redact(s string) string {
	r.mu.RLock()
	secrets := append([]string(nil), r.order...)
	labels := make(map[string]string, len(r.secrets))
	for k, v := range r.secrets {
		labels[k] = v
	}
	r.mu.RUnlock()
	sort.Slice(secrets, func(i, j int) bool { return len(secrets[i]) > len(secrets[j]) })
	for _, v := range secrets {
		s = strings.ReplaceAll(s, v, "["+labels[v]+"]")
	}

	s = reWinQuoted.ReplaceAllStringFunc(s, func(q string) string { return q[:1] + "[PATH]" + q[:1] })
	s = reWinPath.ReplaceAllString(s, "[PATH]")
	s = reUnixPath.ReplaceAllString(s, "[PATH]")
	s = reAuth.ReplaceAllString(s, "${1}[TOKEN]")
	s = reSubproto.ReplaceAllString(s, "token.[TOKEN]")
	s = reTokenKV.ReplaceAllString(s, "${1}${2}[REDACTED]")
	s = reLongHex.ReplaceAllString(s, "[HEX]")
	s = rePairing.ReplaceAllString(s, "[CODE]")
	for _, id := range identity() {
		s = replaceFold(s, id, "[USER]")
	}
	return s
}

var (
	identityOnce sync.Once
	identityVals []string
)

// identity returns the user and host names of this machine (3 characters or more).
func identity() []string {
	identityOnce.Do(func() {
		add := func(v string) {
			if i := strings.LastIndexAny(v, `\/`); i >= 0 { // DOMAIN\user
				v = v[i+1:]
			}
			if len(v) >= 3 {
				identityVals = append(identityVals, v)
			}
		}
		if u, err := user.Current(); err == nil {
			add(u.Username)
			add(filepath.Base(u.HomeDir))
		}
		if h, err := os.Hostname(); err == nil {
			add(h)
		}
	})
	return identityVals
}

// replaceFold replaces every case-insensitive occurrence of the word old in s: never inside
// another word, as "ali" in "alignment" (audit 13, #156).
func replaceFold(s, old, repl string) string {
	if old == "" {
		return s
	}
	lower, lowOld := strings.ToLower(s), strings.ToLower(old)
	if len(lower) != len(s) || len(lowOld) != len(old) { // non-ASCII case mapping changed lengths
		lower, lowOld = s, old // exact case then
	}
	var b strings.Builder
	at := 0 // s[:at] is written
	for from := 0; ; {
		i := strings.Index(lower[from:], lowOld)
		if i < 0 {
			b.WriteString(s[at:])
			return b.String()
		}
		i += from
		end := i + len(old)
		before, _ := utf8.DecodeLastRuneInString(s[:i])
		after, _ := utf8.DecodeRuneInString(s[end:])
		if isWordRune(before) || isWordRune(after) {
			from = i + 1
			continue
		}
		b.WriteString(s[at:i])
		b.WriteString(repl)
		at, from = end, end
	}
}

// isWordRune reports whether r continues a word; utf8.RuneError (no rune) does not.
func isWordRune(r rune) bool {
	return r != utf8.RuneError && (unicode.IsLetter(r) || unicode.IsDigit(r) || r == '_')
}
