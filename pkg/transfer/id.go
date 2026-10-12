// Package transfer is layer E of the bible (chapter 8): the transfer protocol between two
// devices. It starts with the device's single port (CHAN-01): the Hub routes every secure
// connection by its first frame, CONTROL to the session and pairing code, DATA_JOIN to the
// transfer that connection names (CHAN-06). It does not depend on pkg/api (plan J4a, D1).
package transfer

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"fmt"
)

// ID identifies a transfer on its data connections: 128 random bits (bible §8.3).
type ID [16]byte

// NewID draws a transfer id with crypto/rand.
func NewID() (ID, error) {
	var id ID
	_, err := rand.Read(id[:])
	return id, err
}

// String is the hex form carried by control messages.
func (id ID) String() string { return hex.EncodeToString(id[:]) }

// IsZero reports whether id is unset.
func (id ID) IsZero() bool { return id == ID{} }

// ParseID reads the hex form of an id.
func ParseID(s string) (ID, error) {
	var id ID
	b, err := hex.DecodeString(s)
	if err != nil || len(b) != len(id) {
		return id, fmt.Errorf("transfer: invalid transfer id %q", s)
	}
	copy(id[:], b)
	return id, nil
}

// Item derives the id of item i of a batch. Until WP-4.3 carries all the items of an offer on
// the same data connections, each item is a transfer of its own; both sides derive its id
// from the batch's, so that no extra message is needed.
func (id ID) Item(i int) ID {
	h := sha256.New()
	h.Write([]byte("medxfer-v1-item"))
	h.Write(id[:])
	var n [8]byte
	binary.BigEndian.PutUint64(n[:], uint64(i))
	h.Write(n[:])
	var item ID
	copy(item[:], h.Sum(nil))
	return item
}
