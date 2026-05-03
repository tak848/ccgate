//go:build !unix

package keystore

import (
	"context"
	"errors"
	"testing"
)

// On non-Unix builds the package is a stub: every Resolve call must
// return ErrUnsupported with reason "unsupported_platform" so the
// runner falls through gracefully (the equivalent of "no key
// available, fire the upstream prompt") instead of failing
// validation. Invalidate is also a no-op so 401 handling on the
// runner side stays uniform across platforms.
func TestStubResolveAndInvalidate(t *testing.T) {
	if Supported {
		t.Skip("test guards the non-unix stub")
	}
	res, err := Resolve(context.Background(), Options{Command: "anything"})
	if !errors.Is(err, ErrUnsupported) {
		t.Fatalf("err = %v, want ErrUnsupported", err)
	}
	if res.Reason != ReasonUnsupportedPlatform {
		t.Fatalf("reason = %q, want %q", res.Reason, ReasonUnsupportedPlatform)
	}
	if err := Invalidate(Options{Command: "anything"}); err != nil {
		t.Fatalf("invalidate stub returned %v", err)
	}
}
