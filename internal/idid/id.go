// Package idid generates opaque, unguessable identifiers.
//
// Every identifier Storvia hands to a client is generated here from a CSPRNG. No
// identifier encodes a tenant, a user, a file name, or a sequence number, because
// anything guessable invites enumeration and anything descriptive leaks context
// into logs and URLs.
package idid

import (
	"crypto/rand"
	"encoding/base32"
	"fmt"
	"strings"
	"sync/atomic"
	"time"
)

// encoding is Crockford-ish base32 without padding: URL safe, case insensitive,
// and free of characters that need escaping.
var encoding = base32.NewEncoding("0123456789abcdefghjkmnpqrstvwxyz").WithPadding(base32.NoPadding)

// Generator produces prefixed identifiers.
type Generator struct {
	// prefix is prepended with an underscore, for example "obj".
	prefix string
	// counter makes identifiers sortable by creation time within one process,
	// which helps debugging without exposing a count to clients.
	counter *atomic.Uint64
	// now is injectable so tests are deterministic.
	now func() time.Time
}

// NewGenerator builds a generator for a kind of identifier.
func NewGenerator(prefix string) *Generator {
	return &Generator{
		prefix:  strings.ToLower(prefix),
		counter: &atomic.Uint64{},
		now:     time.Now,
	}
}

// New returns a new identifier such as obj_01h8x9k2m4.
//
// The shape is <prefix>_<base32 millis><base32 counter><base32 random>. The random
// suffix is what makes it unguessable; the prefix and timestamp are for operators.
func (g *Generator) New() (string, error) {
	random := make([]byte, 10)
	if _, err := rand.Read(random); err != nil {
		return "", fmt.Errorf("idid: generate random: %w", err)
	}
	counter := g.counter.Add(1)

	var b strings.Builder
	b.WriteString(g.prefix)
	b.WriteByte('_')

	var millis [8]byte
	ts := uint64(g.now().UnixMilli())
	for i := 7; i >= 0; i-- {
		millis[i] = byte(ts)
		ts >>= 8
	}
	b.WriteString(encoding.EncodeToString(millis[:]))

	var count [4]byte
	c := uint32(counter)
	for i := 3; i >= 0; i-- {
		count[i] = byte(c)
		c >>= 8
	}
	b.WriteString(encoding.EncodeToString(count[:]))
	b.WriteString(encoding.EncodeToString(random))
	return b.String(), nil
}

// MustNew is New for paths where a failure means the process cannot start.
func (g *Generator) MustNew() string {
	id, err := g.New()
	if err != nil {
		panic(fmt.Sprintf("idid: %v", err))
	}
	return id
}
