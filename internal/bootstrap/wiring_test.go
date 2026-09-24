package bootstrap

import (
	"strings"
	"testing"

	"github.com/eandstravel/carwash/internal/api"
	"github.com/eandstravel/carwash/internal/service"
)

// The bug this guards against: Media was built and never assigned, so the
// literal compiled, the tests passed, and the routes answered 500. The check
// has to name the field, because "something is nil" sends whoever reads the
// log back into the wiring to find out which.
func TestRequireWiredNamesTheMissingDependency(t *testing.T) {
	err := requireWired(api.Deps{})
	if err == nil {
		t.Fatal("an empty Deps was accepted — the check is not looking at anything")
	}
	for _, want := range []string{"Media", "Config", "Log"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error does not name %s: %v", want, err)
		}
	}
}

func TestRequireWiredIgnoresFieldsThatAreSet(t *testing.T) {
	// One field set, and it must no longer be reported. Cheap, but it is the
	// half that would let a check that reports everything pass the test above.
	svc, err := service.NewMediaService(nil, "", 0)
	if err != nil {
		t.Fatalf("media service: %v", err)
	}
	got := requireWired(api.Deps{Media: svc})
	if got == nil {
		t.Fatal("want the other fields still reported")
	}
	if strings.Contains(got.Error(), "Media") {
		t.Errorf("Media was set but still reported missing: %v", got)
	}
}
