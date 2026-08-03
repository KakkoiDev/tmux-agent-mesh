package store

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"strconv"
	"strings"
)

// MessageUID is a message's identity outside this database.
//
// Row ids are local: machine A's message 7 and machine B's message 7 are
// different messages, so an id cannot be what `import` deduplicates on. Hashing
// the content gives an identifier both machines compute the same way, which is
// what makes syncing a set union: importing the same row twice is INSERT OR
// IGNORE hitting the same uid, and order does not matter.
//
// Channel and thread are hashed by name rather than by id for the same reason.
//
// The nonce is what keeps this an identifier rather than a collision: two
// identical bodies posted in the same second by the same agent into the same
// thread are two distinct messages, and without a nonce the second would hash
// to the first and be silently dropped on import.
//
// Fields are joined with a unit separator, which cannot occur in any of them, so
// no field can be crafted to look like a different set of fields.
func MessageUID(nonce, channel, thread, from, body string, createdAt int64, replyToUID string) string {
	h := sha256.New()
	h.Write([]byte(strings.Join([]string{
		nonce, channel, thread, from, body,
		strconv.FormatInt(createdAt, 10), replyToUID,
	}, "\x1f")))
	return hex.EncodeToString(h.Sum(nil))
}

// NewNonce is 16 random bytes, hex encoded.
func NewNonce() string {
	b := make([]byte, 16)
	if _, err := rand.Read(b); err != nil {
		// crypto/rand does not fail on any platform this runs on, and a message
		// without a nonce would silently collide with an identical one rather
		// than fail loudly, so there is nothing safe to fall back to.
		panic("mesh: no entropy for a message nonce: " + err.Error())
	}
	return hex.EncodeToString(b)
}
