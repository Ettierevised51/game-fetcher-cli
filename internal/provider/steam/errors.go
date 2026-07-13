package steam

import (
	"strings"

	"github.com/Austrum-lab/game-fetcher-cli/internal/retry"
)

// Patterns are matched case-insensitively against the combined steamcmd
// output (spec section 5).
var (
	fatalPatterns = []string{
		"no subscription",
		"invalid password",
		"invalid platform",
		"restricted user account",
		// Steam Guard wants a one-time code — retrying without one is
		// hopeless; EnsureLogin handles the interactive entry instead.
		"account logon denied",
		"not been authenticated",
		// A mistyped Steam Guard code; retrying the same one cannot help.
		"invalid login auth code",
		"two-factor code mismatch",
	}
	rateLimitPatterns = []string{
		"rate limit exceeded",
	}
)

// loginFailedPatterns detect a steamcmd session that never got logged in:
// "FAILED login with result code ..." and the anonymous variant
// "Connecting anonymously to Steam Public...FAILED".
var loginFailedPatterns = []string{
	"failed login",
	"login failure",
	"steam public...failed",
}

// loginFailed reports whether the steamcmd output shows the login never
// succeeded. Matters for batch runs (@ShutdownOnFailedCommand 0): the
// commands after a failed login are meaningless, and content left on disk by
// earlier runs must not count as success.
func loginFailed(out string) bool {
	l := strings.ToLower(out)
	for _, p := range loginFailedPatterns {
		if strings.Contains(l, p) {
			return true
		}
	}
	return false
}

// guardPatterns mean this machine has no cached Steam Guard sentry yet and
// the login needs a one-time interactive code entry.
var guardPatterns = []string{
	"steam guard",
	"account logon denied",
	"not been authenticated",
	"two-factor",
}

func needsGuardCode(out string) bool {
	l := strings.ToLower(out)
	for _, p := range guardPatterns {
		if strings.Contains(l, p) {
			return true
		}
	}
	return false
}

// classifyOutput maps a failed steamcmd run to a retry class. Anything not
// recognized as fatal or rate-limited — CDN timeouts, generic "Failure",
// connection drops — is retryable.
func classifyOutput(out string) retry.Class {
	l := strings.ToLower(out)
	for _, p := range fatalPatterns {
		if strings.Contains(l, p) {
			return retry.Fatal
		}
	}
	for _, p := range rateLimitPatterns {
		if strings.Contains(l, p) {
			return retry.RateLimited
		}
	}
	return retry.Retryable
}
