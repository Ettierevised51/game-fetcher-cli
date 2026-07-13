package steam

import (
	"testing"

	"github.com/Austrum-lab/game-fetcher-cli/internal/retry"
)

func TestClassifyOutput(t *testing.T) {
	cases := []struct {
		out  string
		want retry.Class
	}{
		// Fatal: retrying cannot help (spec section 5).
		{"ERROR! Failed to install app '730' (No subscription)", retry.Fatal},
		{"FAILED login with result code Invalid Password", retry.Fatal},
		// Login rate limit: retryable, but only after a heavily increased delay.
		{"FAILED login with result code Rate Limit Exceeded", retry.RateLimited},
		// Everything else — timeouts, CDN hiccups, generic failures — retries.
		{"ERROR! Timeout downloading item 123456", retry.Retryable},
		{"Error! App '730' state is 0x602 after update job.", retry.Retryable},
		{"[----] Update job failed: Failure", retry.Retryable},
		{"", retry.Retryable},
	}
	for _, tc := range cases {
		if got := classifyOutput(tc.out); got != tc.want {
			t.Errorf("classifyOutput(%q) = %v, want %v", tc.out, got, tc.want)
		}
	}
}
