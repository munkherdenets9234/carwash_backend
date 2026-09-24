package models

import (
	"crypto/rand"
	"fmt"
	"strings"
)

// referenceAlphabet is what a booking code is drawn from.
//
// It is not the full alphabet, and the omissions are the point. A reference
// is read off a phone screen by one person and typed, or spoken down a
// telephone line and written down by another, and these are the pairs people
// confuse in exactly that situation:
//
//	0 / O, 1 / I / L, 2 / Z, 5 / S, 8 / B
//
// BOTH members of every pair are excluded, not one. Dropping only one would
// leave the survivor to be mistyped as the character that is no longer
// there; dropping both means no code can contain either, so a confusion
// cannot produce a code that is wrong-but-plausible — it produces one that
// cannot exist, and the lookup says so. U is out as well, so the generator
// cannot spell anything unfortunate.
//
// What is left is 24 symbols. Eight of them is 24^8, about 1.1e11. With the
// phone number also required and the endpoint rate limited, guessing one is
// not a way in.
const referenceAlphabet = "34679ACDEFGHJKMNPQRTVWXY"

// ReferenceLength is the number of symbols in a stored booking code. The
// dash people see is inserted for display and is not part of the value.
const ReferenceLength = 8

// NewReference returns a stored booking code, such as "4KQPM7HX".
//
// This is the only thing a guest is given to find their booking again, so it
// comes from crypto/rand and not math/rand. A predictable code would mean
// somebody who books once can compute other people's, and what they would
// read back is a name, a car plate, a time and a location — enough to know
// when a particular car is away from a particular place.
//
// An error from the system's randomness is returned rather than swallowed by
// a fallback. A fallback here would issue a weaker code, silently, at exactly
// the moment the strong one was unavailable.
func NewReference() (string, error) {
	b := make([]byte, ReferenceLength)
	if _, err := rand.Read(b); err != nil {
		return "", fmt.Errorf("booking reference: %w", err)
	}

	out := make([]byte, ReferenceLength)
	for i, v := range b {
		// The modulo bias is negligible: 256 over 24 symbols favours the
		// first sixteen by about a quarter of a percent, which does not
		// meaningfully change how hard a code is to guess. Stated so the
		// next reader does not have to work out whether it matters.
		out[i] = referenceAlphabet[int(v)%len(referenceAlphabet)]
	}
	return string(out), nil
}

// NormalizeReference puts a typed code into the form it is stored in.
//
// People type them in lower case, leave the dash out, add spaces, or paste
// them with the dash where they saw it. All of those are the same code, and
// a lookup that refuses them tells a customer their own booking does not
// exist.
//
// It does not try to repair confusable characters, because the alphabet
// above already made that impossible: no generated code contains either half
// of any confusable pair, so a "0" or an "I" in typed input cannot be a
// mistyping of something real. It is simply a code that does not exist, and
// the honest answer is that it was not found.
func NormalizeReference(s string) string {
	var b strings.Builder
	for _, r := range strings.ToUpper(strings.TrimSpace(s)) {
		if r == '-' || r == ' ' || r == '\t' {
			continue
		}
		b.WriteRune(r)
	}
	return b.String()
}

// FormatReference inserts the dash, for showing a stored code to a person.
// Splitting eight characters into two groups of four is what makes it
// readable aloud and re-typable without losing your place.
func FormatReference(s string) string {
	if len(s) != ReferenceLength {
		return s
	}
	return s[:ReferenceLength/2] + "-" + s[ReferenceLength/2:]
}
