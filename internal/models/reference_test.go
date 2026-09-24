package models

import (
	"strings"
	"testing"

	"go.mongodb.org/mongo-driver/bson/primitive"
)

func TestReferenceAlphabetExcludesBothHalvesOfEveryConfusablePair(t *testing.T) {
	// The guarantee NormalizeReference relies on: because neither half of a
	// confusable pair can appear in a code, typed input containing one is
	// definitely wrong rather than possibly-a-typo. If somebody adds a
	// character back to the alphabet, that reasoning silently stops holding
	// and this test is what says so.
	for _, r := range "01258OILZSBU" {
		if strings.ContainsRune(referenceAlphabet, r) {
			t.Errorf("%q is in the alphabet — it is confusable with another character, "+
				"and NormalizeReference assumes neither half of a pair can occur", r)
		}
	}
}

func TestReferencesAreDistinct(t *testing.T) {
	seen := make(map[string]bool, 2000)
	for i := 0; i < 2000; i++ {
		got, err := NewReference()
		if err != nil {
			t.Fatalf("NewReference: %v", err)
		}
		if len(got) != ReferenceLength {
			t.Fatalf("got %q (%d chars), want %d", got, len(got), ReferenceLength)
		}
		for _, r := range got {
			if !strings.ContainsRune(referenceAlphabet, r) {
				t.Fatalf("%q contains %q, which is not in the alphabet", got, r)
			}
		}
		if seen[got] {
			// Not a birthday-paradox flake: 2000 draws from 1.1e11 collide
			// with probability about 2e-5. A repeat here means the generator
			// is not random, which would make every code guessable.
			t.Fatalf("%q was generated twice in 2000 draws — the generator is not random", got)
		}
		seen[got] = true
	}
}

func TestNormalizeReferenceAcceptsHowPeopleActuallyTypeIt(t *testing.T) {
	const stored = "4KQPM7HX"
	for _, in := range []string{
		"4KQPM7HX",
		"4KQP-M7HX",
		"4kqp-m7hx",
		"  4KQP M7HX  ",
		"4kqpm7hx",
	} {
		if got := NormalizeReference(in); got != stored {
			t.Errorf("NormalizeReference(%q) = %q, want %q", in, got, stored)
		}
	}
}

func TestFormatReferenceRoundTrips(t *testing.T) {
	ref, err := NewReference()
	if err != nil {
		t.Fatalf("NewReference: %v", err)
	}
	shown := FormatReference(ref)
	if !strings.Contains(shown, "-") {
		t.Errorf("FormatReference(%q) = %q, want a dash for readability", ref, shown)
	}
	if got := NormalizeReference(shown); got != ref {
		t.Errorf("what is shown does not normalise back: showed %q, got %q, want %q", shown, got, ref)
	}
}

func TestNormalizePhoneCollapsesHowPeopleWriteANumber(t *testing.T) {
	// The same telephone, seven ways. Without this the business sees seven
	// customers with one visit each instead of one with seven — and somebody
	// who types their number differently the second time is told their own
	// booking does not exist.
	//
	// The last three are the ones that were wrong: an earlier version kept
	// the country code, so +976 9911 2233 and 9911 2233 were two people.
	for _, in := range []string{
		"99112233",
		"9911-2233",
		"9911 2233",
		" 99 11 22 33 ",
		"+976 9911 2233",
		"976 9911 2233",
		"+97699112233",
	} {
		if got := NormalizePhone(in); got != "99112233" {
			t.Errorf("NormalizePhone(%q) = %q, want 99112233", in, got)
		}
	}
}

func TestNormalizePhoneLeavesForeignNumbersDialable(t *testing.T) {
	// Stripping a leading "976" unconditionally would mangle any number that
	// happens to begin with those digits. The length check is what stops it,
	// and this is the case that proves the check is doing something.
	for in, want := range map[string]string{
		"+1 976 555 0134":  "19765550134",  // starts with 976 after the 1
		"9765550134":       "9765550134",   // ten digits: not the local shape
		"+44 20 7946 0958": "442079460958", // ordinary foreign number
	} {
		if got := NormalizePhone(in); got != want {
			t.Errorf("NormalizePhone(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestContactKeyIsTheSameForOneTelephone(t *testing.T) {
	// The consequence of the above, at the level that actually matters: two
	// spellings of one number must produce one customer.
	id := primitive.NewObjectID()
	a := ContactKey(id, "9911-2233")
	b := ContactKey(id, "+976 9911 2233")
	if a != b {
		t.Errorf("one telephone gave two contact keys:\n  %q\n  %q", a, b)
	}
	if a == "" {
		t.Error("contact key is empty for a real number")
	}
}
