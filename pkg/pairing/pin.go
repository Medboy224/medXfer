package pairing

import (
	"crypto/rand"
	"errors"
	"fmt"
	"io"
	"math/big"
	"strings"
	"sync"
	"time"
)

// Defaults of the PIN path (bible §6.2 and §6.4, decisions D4 and D5 of the J5 plan; all are
// parameters, rule R4).
const (
	PINDigits      = 6
	PINAttempts    = 3                // PAIR-04
	PINDelay       = 30 * time.Second // after PINAttempts PINs destroyed in a row (D5)
	PINMaxDelay    = 10 * time.Minute
	minPINDigits   = 6
	maxPINDigits   = 8
	pinDestroyedIn = 3 // PINs destroyed in a row before a delay (PAIR-04)
)

// ErrBadPIN: the PIN typed is not 6 to 8 digits. It is a local error: nothing was sent.
var ErrBadPIN = errors.New("pairing: a PIN is 6 to 8 digits")

// PINConfig sets a PIN's parameters; zero values take the defaults.
type PINConfig struct {
	Digits   int           // PINDigits; 8 is the stronger option (D4)
	TTL      time.Duration // TTL, as a QR code (PAIR-02)
	Attempts int           // PINAttempts
	Delay    time.Duration // PINDelay, doubled at each new series, up to MaxDelay
	MaxDelay time.Duration // PINMaxDelay
	// Show is called with each new PIN and when it expires; with "" when no PIN is valid,
	// expires then being when the next one appears. Calls come one at a time, from any
	// goroutine. The PIN is for the local screen only: it is never logged (bible chapter 13).
	Show func(code string, expires time.Time)
	// Rand defaults to crypto/rand (PAIR-01).
	Rand io.Reader
}

// PIN is the responder's side of the PIN path (bible §6.2): the PIN on screen, the attempts
// made on it, and the next PIN when it is destroyed (PAIR-04). Each connection tests one PIN
// only: CPace leaves no offline attack (D3). It is safe for concurrent use.
type PIN struct {
	cfg    PINConfig
	showMu sync.Mutex

	mu        sync.Mutex
	code      []byte    // ASCII digits; nil while none is valid
	expires   time.Time // of code, or of the next one while waiting
	reserved  int       // attempts begun on code
	failed    int       // attempts on code that did not pair
	destroyed int       // PINs destroyed in a row
	delay     time.Duration
	redraw    *time.Timer
	paired    bool
	closed    bool
}

// NewPIN draws the first PIN and shows it.
func NewPIN(c PINConfig) (*PIN, error) {
	if c.Digits == 0 {
		c.Digits = PINDigits
	}
	if c.Digits < minPINDigits || c.Digits > maxPINDigits {
		return nil, ErrBadPIN
	}
	if c.TTL <= 0 {
		c.TTL = TTL
	}
	if c.Attempts <= 0 {
		c.Attempts = PINAttempts
	}
	if c.Delay <= 0 {
		c.Delay = PINDelay
	}
	if c.MaxDelay <= 0 {
		c.MaxDelay = PINMaxDelay
	}
	if c.Rand == nil {
		c.Rand = rand.Reader
	}
	p := &PIN{cfg: c, delay: c.Delay}
	p.mu.Lock()
	err := p.drawLocked()
	code, exp := string(p.code), p.expires
	p.mu.Unlock()
	if err != nil {
		return nil, err
	}
	p.show(code, exp)
	return p, nil
}

// drawPIN is a uniform PIN of n digits: crypto/rand.Int draws in [0, 10^n) without modulo
// bias (PAIR-01).
func drawPIN(r io.Reader, n int) ([]byte, error) {
	max := new(big.Int).Exp(big.NewInt(10), big.NewInt(int64(n)), nil)
	v, err := rand.Int(r, max)
	if err != nil {
		return nil, err
	}
	return []byte(fmt.Sprintf("%0*d", n, v.Int64())), nil
}

func (p *PIN) drawLocked() error {
	code, err := drawPIN(p.cfg.Rand, p.cfg.Digits)
	if err != nil {
		return err
	}
	p.code, p.expires, p.reserved, p.failed = code, time.Now().Add(p.cfg.TTL), 0, 0
	return nil
}

func (p *PIN) show(code string, expires time.Time) {
	if p.cfg.Show == nil {
		return
	}
	p.showMu.Lock()
	defer p.showMu.Unlock()
	p.cfg.Show(code, expires)
}

// deadline is when the responder stops waiting: the current PIN's expiry, or the next one's
// while a delay runs.
func (p *PIN) deadline() time.Time {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.expires
}

// begin reserves an attempt on the current PIN and returns it. The attempt counts from here:
// once the responder has answered, the peer has tested its guess, whether it confirms or not.
// ok is false when no attempt is left, no PIN is valid, or the PIN is used: the caller then
// runs the exchange on a decoy, so that the peer cannot tell (PAIR-07).
func (p *PIN) begin() (code []byte, ok bool) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.closed || p.paired || p.code == nil || !time.Now().Before(p.expires) || p.reserved >= p.cfg.Attempts {
		return nil, false
	}
	p.reserved++
	return append([]byte(nil), p.code...), true
}

// end closes an attempt begun by begin. A success uses the PIN (PAIR-02) and resets the
// delay; the last failure destroys the PIN and draws another, after a delay once
// pinDestroyedIn PINs were destroyed in a row (PAIR-04, D5).
func (p *PIN) end(paired bool) {
	p.mu.Lock()
	if paired {
		p.paired, p.code, p.destroyed, p.delay = true, nil, 0, p.cfg.Delay
		p.mu.Unlock()
		return
	}
	p.failed++
	if p.closed || p.failed < p.cfg.Attempts {
		p.mu.Unlock()
		return
	}
	p.code = nil
	p.destroyed++
	if p.destroyed%pinDestroyedIn != 0 {
		err := p.drawLocked()
		code, exp := string(p.code), p.expires
		p.mu.Unlock()
		if err == nil {
			p.show(code, exp)
		}
		return
	}
	wait := p.delay
	p.delay = min(2*p.delay, p.cfg.MaxDelay)
	p.expires = time.Now().Add(wait + p.cfg.TTL)
	exp := time.Now().Add(wait)
	p.redraw = time.AfterFunc(wait, p.redrawAfterDelay)
	p.mu.Unlock()
	p.show("", exp)
}

func (p *PIN) redrawAfterDelay() {
	p.mu.Lock()
	if p.closed || p.paired {
		p.mu.Unlock()
		return
	}
	err := p.drawLocked()
	code, exp := string(p.code), p.expires
	p.mu.Unlock()
	if err == nil {
		p.show(code, exp)
	}
}

// Close ends the PIN: no attempt is accepted any more and no PIN is drawn. Serve closes the
// PIN it was given when it returns.
func (p *PIN) Close() {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.closed, p.code = true, nil
	if p.redraw != nil {
		p.redraw.Stop()
	}
}

// NormalizePIN returns the digits of a PIN typed with spaces or dashes ("123 456").
func NormalizePIN(s string) ([]byte, error) {
	var out []byte
	for _, r := range s {
		switch {
		case r >= '0' && r <= '9':
			out = append(out, byte(r))
		case r == ' ' || r == '-':
		default:
			return nil, ErrBadPIN
		}
	}
	if len(out) < minPINDigits || len(out) > maxPINDigits {
		return nil, ErrBadPIN
	}
	return out, nil
}

// FormatPIN groups a PIN for the screen: "123 456", "1234 5678".
func FormatPIN(code string) string {
	g := 3
	if len(code)%4 == 0 {
		g = 4
	}
	var sb strings.Builder
	for i := 0; i < len(code); i += g {
		if i > 0 {
			sb.WriteByte(' ')
		}
		sb.WriteString(code[i:min(i+g, len(code))])
	}
	return sb.String()
}
