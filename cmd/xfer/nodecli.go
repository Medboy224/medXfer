package main

import (
	"context"
	"errors"
	"strings"
	"unicode"

	"github.com/Medboy224/medXfer/pkg/api"
)

// announcer is what xfer node needs of its discovery server.
type announcer interface {
	SetTags(f func() []string)
	Start(ctx context.Context)
}

// startAnnouncing starts the announcements with their tags already set: the first one carries
// them, instead of none until the next one, 30 s later (#337).
func startAnnouncing(ctx context.Context, a announcer, tags func() []string) {
	a.SetTags(tags)
	a.Start(ctx)
}

// nodeReceiveDir is where xfer node saves what it receives: the configured download folder,
// not the current one (#314).
func nodeReceiveDir() string {
	return api.LoadConfig("", "").DownloadDir
}

var errOpenQuote = errors.New("a quote is not closed")

// splitArgs splits a command line into words at spaces, except between double quotes (#314:
// send "My documents/x.pdf"). An apostrophe and a backslash stay characters: l'été.jpg, and
// the separator of Windows paths.
func splitArgs(line string) ([]string, error) {
	var words []string
	var word strings.Builder
	inWord, quoted := false, false
	for _, r := range line {
		switch {
		case r == '"':
			quoted, inWord = !quoted, true
		case quoted || !unicode.IsSpace(r):
			word.WriteRune(r)
			inWord = true
		case inWord:
			words = append(words, word.String())
			word.Reset()
			inWord = false
		}
	}
	if quoted {
		return nil, errOpenQuote
	}
	if inWord {
		words = append(words, word.String())
	}
	return words, nil
}
