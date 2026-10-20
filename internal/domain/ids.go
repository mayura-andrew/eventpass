// SPDX-License-Identifier: Apache-2.0

package domain

import (
	"crypto/rand"
	"regexp"
	"strings"

	"github.com/oklog/ulid/v2"
)

// passAlphabet has no characters people confuse when reading a pass aloud
// or off a screen: no 0/O, 1/I/L, or U/V.
const passAlphabet = "23456789ABCDEFGHJKMNPQRSTWXYZ"

var passIDRE = regexp.MustCompile(`^EP-[` + passAlphabet + `]{4}-[` + passAlphabet + `]{4}$`)

// NewPassID returns a pass ID like EP-7KQ4-M2XD.
func NewPassID() string {
	var b [8]byte
	if _, err := rand.Read(b[:]); err != nil {
		panic(err) // crypto/rand never fails on supported platforms
	}
	var sb strings.Builder
	sb.WriteString("EP-")
	for i, x := range b {
		if i == 4 {
			sb.WriteByte('-')
		}
		// 256 % 29 bias is under 1%; fine for an identifier that is not a secret.
		sb.WriteByte(passAlphabet[int(x)%len(passAlphabet)])
	}
	return sb.String()
}

// ValidPassID reports whether s looks like a pass ID. Lower case and missing
// dashes are accepted by NormalizePassID first.
func ValidPassID(s string) bool { return passIDRE.MatchString(s) }

// NormalizePassID turns what staff type ("ep7kq4m2xd") into EP-7KQ4-M2XD.
func NormalizePassID(s string) string {
	s = strings.ToUpper(strings.NewReplacer("-", "", " ", "").Replace(s))
	if len(s) == 10 && strings.HasPrefix(s, "EP") {
		return "EP-" + s[2:6] + "-" + s[6:]
	}
	return s
}

// NewID returns a ULID: sortable by creation time, unique without
// coordination, readable enough in a spreadsheet cell.
func NewID() string { return ulid.Make().String() }
