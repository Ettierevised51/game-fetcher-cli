package steam

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/Austrum-lab/game-fetcher-cli/internal/retry"
)

const guardOutput = `Logging in user 'gabe' to Steam Public...
This computer has not been authenticated for your account using Steam Guard.
ERROR (Account Logon Denied)`

func TestNeedsGuardCode(t *testing.T) {
	if !needsGuardCode(guardOutput) {
		t.Error("Account Logon Denied must be recognized as a Steam Guard prompt")
	}
	if needsGuardCode("FAILED login with result code Invalid Password") {
		t.Error("a wrong password is not a Steam Guard situation")
	}
}

func TestClassifyLogonDeniedFatal(t *testing.T) {
	if classifyOutput(guardOutput) != retry.Fatal {
		t.Error("Account Logon Denied must never be retried blindly")
	}
}

func TestEnsureLoginAnonymousIsNoop(t *testing.T) {
	ran := false
	p := New(Options{SteamcmdPath: "unused", Run: func(context.Context, string, ...string) (string, error) {
		ran = true
		return "", nil
	}})
	if err := p.EnsureLogin(context.Background()); err != nil || ran {
		t.Fatalf("anonymous EnsureLogin must do nothing, got err=%v ran=%v", err, ran)
	}
}

// Under `go test` stdin is not a terminal, so the Guard branch must fail
// fatally with the how-to instead of trying to prompt.
func TestEnsureLoginGuardWithoutTerminal(t *testing.T) {
	p := New(Options{
		SteamcmdPath: fakeBin(t),
		Credentials:  Credentials{Username: "gabe", Password: "s3cret"},
		Run: func(context.Context, string, ...string) (string, error) {
			return guardOutput, errors.New("exit status 5")
		},
	})
	err := p.EnsureLogin(context.Background())
	if err == nil {
		t.Fatal("expected an error when Steam Guard wants a code and there is no terminal")
	}
	if retry.ClassOf(err) != retry.Fatal {
		t.Errorf("must be fatal, got class %v", retry.ClassOf(err))
	}
	if !strings.Contains(err.Error(), "health-check") {
		t.Errorf("error must point at the interactive fix, got: %v", err)
	}
}

func TestEnsureLoginPlainFailure(t *testing.T) {
	p := New(Options{
		SteamcmdPath: fakeBin(t),
		Credentials:  Credentials{Username: "gabe", Password: "wrong"},
		Run: func(context.Context, string, ...string) (string, error) {
			return "FAILED login with result code Invalid Password", errors.New("exit status 5")
		},
	})
	err := p.EnsureLogin(context.Background())
	if err == nil || retry.ClassOf(err) != retry.Fatal {
		t.Fatalf("invalid password must be a fatal login error, got %v", err)
	}
}
