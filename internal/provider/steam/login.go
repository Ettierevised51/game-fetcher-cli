package steam

import (
	"errors"
	"fmt"
	"os"
	"strings"

	"golang.org/x/term"
)

const (
	envUsername = "GAMEFETCHER_STEAM_USERNAME"
	envPassword = "GAMEFETCHER_STEAM_PASSWORD"
)

// Credentials is a Steam login; the zero value means anonymous.
// Passwords are never read from the YAML config (spec section 10) — only
// from an explicit flag, the environment, or an interactive prompt.
type Credentials struct {
	Username string
	Password string
}

func (c Credentials) Anonymous() bool { return c.Username == "" }

// ResolveCredentials resolves the login per spec section 10: explicit values
// (CLI flags) win, then GAMEFETCHER_STEAM_USERNAME/GAMEFETCHER_STEAM_PASSWORD,
// and a still-missing password is asked for interactively with hidden input.
// getenv defaults to os.Getenv and input to os.Stdin (both injectable for
// tests).
// usernameSource labels where an explicit username came from ("--steam-user",
// "config steam_user") so the password prompt can say so; empty means the
// flag.
func ResolveCredentials(username, password, usernameSource string, getenv func(string) string, input *os.File) (Credentials, error) {
	if getenv == nil {
		getenv = os.Getenv
	}
	if input == nil {
		input = os.Stdin
	}
	if usernameSource == "" {
		usernameSource = "--steam-user"
	}
	if username == "" {
		username = getenv(envUsername)
		usernameSource = envUsername
	}
	// An explicit "anonymous" forces the anonymous login even when an env
	// var or config supplies an account (no logout needed for a one-off).
	if strings.EqualFold(username, "anonymous") {
		return Credentials{}, nil
	}
	if password == "" {
		password = getenv(envPassword)
	}
	if username == "" {
		if password != "" {
			return Credentials{}, fmt.Errorf("a Steam password is set but no username; set %s too", envUsername)
		}
		return Credentials{}, nil // anonymous
	}
	if password == "" {
		fd := int(input.Fd())
		if !term.IsTerminal(fd) {
			return Credentials{}, fmt.Errorf("no password for %s and stdin is not a terminal; set %s", username, envPassword)
		}
		// Naming the username's source saves head-scratching when a
		// forgotten exported env var triggers this prompt.
		fmt.Fprintf(os.Stderr, "Steam password for %s (username from %s): ", username, usernameSource)
		raw, err := term.ReadPassword(fd)
		fmt.Fprintln(os.Stderr)
		if err != nil {
			return Credentials{}, fmt.Errorf("reading password: %w", err)
		}
		password = string(raw)
	}
	creds := Credentials{Username: username, Password: password}
	if err := creds.validate(); err != nil {
		return Credentials{}, err
	}
	return creds, nil
}

// validate rejects values that could break out of the generated runscript.
func (c Credentials) validate() error {
	if c.Anonymous() {
		return nil
	}
	for _, r := range c.Username {
		if r <= ' ' || r == 0x7f || r == '"' {
			return errors.New("steam username contains whitespace, quotes or control characters")
		}
	}
	for _, r := range c.Password {
		if r < ' ' || r == 0x7f {
			return errors.New("steam password contains control characters")
		}
		// steamcmd's runscript quoting rules for these are unverified; a
		// misparsed login line must not fail silently or leak into the
		// command stream.
		if r == '"' || r == '\\' {
			return errors.New(`steam password contains '"' or '\' which steamcmd runscripts cannot carry safely`)
		}
	}
	return nil
}
