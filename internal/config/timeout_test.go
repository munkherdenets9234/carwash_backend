package config

import (
	"os"
	"testing"
	"time"
)

// The bug these cover: ReadTimeout and WriteTimeout were both 15s, and an
// image upload routinely takes longer than that. WriteTimeout is measured
// from the end of the header read, so it covers the whole handler including
// the round trip to the image host. The upload succeeded and the caller was
// told the backend was unreachable.
func TestUploadTimeoutsOutlastAnUpload(t *testing.T) {
	os.Clearenv()

	const observedUpload = 35 * time.Second

	got := timeoutEnv("HTTP_WRITE_TIMEOUT_SEC", 180)
	if got <= observedUpload {
		t.Errorf("default write timeout is %v, which is not longer than an upload observed at %v — "+
			"a successful upload would be reported to the caller as a dropped connection", got, observedUpload)
	}
	if r := timeoutEnv("HTTP_READ_TIMEOUT_SEC", 120); r <= 30*time.Second {
		t.Errorf("default read timeout is %v, too short to receive a large photograph over mobile data", r)
	}
}

func TestZeroTimeoutFallsBackInsteadOfDisablingTheTimeout(t *testing.T) {
	// A zero Duration means NO timeout to net/http. Somebody turning the
	// value down to 0 would be switching the protection off while believing
	// they had tightened it, and nothing would report that.
	for _, raw := range []string{"0", "-5"} {
		t.Setenv("HTTP_WRITE_TIMEOUT_SEC", raw)
		if got := timeoutEnv("HTTP_WRITE_TIMEOUT_SEC", 180); got != 180*time.Second {
			t.Errorf("%s gave %v, want the default — 0 must not mean no timeout", raw, got)
		}
	}
}

func TestTimeoutIsConfigurable(t *testing.T) {
	t.Setenv("HTTP_WRITE_TIMEOUT_SEC", "45")
	if got := timeoutEnv("HTTP_WRITE_TIMEOUT_SEC", 180); got != 45*time.Second {
		t.Errorf("got %v, want 45s — a deployment near its image host must be able to cut this", got)
	}
}
