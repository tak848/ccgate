//go:build !unix

package keystore

import "context"

// Supported is false on non-Unix platforms: the live implementation
// uses BSD-style flock and process-group kill, which Windows / plan9
// / wasip1 / js don't expose with the same semantics. Callers can
// branch on this constant when they want to surface
// `unsupported_platform` early without invoking Resolve.
const Supported = false

// Resolve always reports unsupported_platform on this build. The
// runner uses this to fall through with kind=credential_unavailable
// rather than failing validation, so a Linux/macOS dotfile that
// configures api_key_command does not break the hook on Windows
// — env var fallback continues to work.
func Resolve(_ context.Context, _ Options) (Result, error) {
	return Result{
		Reason: ReasonUnsupportedPlatform,
		Source: SourceCommand,
	}, ErrUnsupported
}

// Invalidate is a no-op on unsupported platforms: there is no cache
// file to remove because Resolve never wrote one.
func Invalidate(_ Options) error {
	return nil
}
