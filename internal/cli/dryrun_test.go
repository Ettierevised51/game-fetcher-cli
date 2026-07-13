package cli

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"gopkg.in/yaml.v3"
)

// execRoot runs the real command tree with the given args and returns stdout.
func execRoot(t *testing.T, args ...string) (string, error) {
	t.Helper()
	cmd := newRootCmd()
	var stdout, stderr bytes.Buffer
	cmd.SetOut(&stdout)
	cmd.SetErr(&stderr)
	cmd.SetArgs(args)
	err := cmd.Execute()
	return stdout.String(), err
}

func testConfig(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	path := filepath.Join(dir, "gamefetcher.yaml")
	content := "state_path: " + filepath.Join(dir, "state.json") + "\n" +
		"profiles:\n  sdk:\n    app_id: 1007\n    install_dir: " + filepath.Join(dir, "install") + "\n"
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestSyncDryRunJSON(t *testing.T) {
	out, err := execRoot(t, "--config", testConfig(t), "sync", "--dry-run", "--json")
	if err != nil {
		t.Fatal(err)
	}
	var plan struct {
		Download []struct {
			Kind string `json:"kind"`
			ID   string `json:"id"`
		} `json:"download"`
		Skip []any `json:"skip"`
	}
	if err := json.Unmarshal([]byte(out), &plan); err != nil {
		t.Fatalf("not JSON: %v\n%s", err, out)
	}
	if len(plan.Download) != 1 || plan.Download[0].ID != "1007" || plan.Download[0].Kind != "app" {
		t.Fatalf("plan = %+v", plan)
	}
}

func TestRunDryRunExplicitFlags(t *testing.T) {
	out, err := execRoot(t, "--config", testConfig(t), "run",
		"--app", "1007", "--dir", filepath.Join(t.TempDir(), "x"), "--dry-run")
	if err != nil {
		t.Fatal(err)
	}
	if out == "" || !bytes.Contains([]byte(out), []byte("app 1007")) {
		t.Fatalf("dry-run plan missing the app: %q", out)
	}
}

// seedAppListCache plants a fresh local index so --game resolution works
// offline in tests.
func seedAppListCache(t *testing.T, cfgPath string, apps string) {
	t.Helper()
	raw, err := os.ReadFile(cfgPath)
	if err != nil {
		t.Fatal(err)
	}
	var cfg struct {
		StatePath string `yaml:"state_path"`
	}
	if err := yaml.Unmarshal(raw, &cfg); err != nil {
		t.Fatal(err)
	}
	cache := `{"fetched_at":"` + time.Now().Format(time.RFC3339) + `","apps":[` + apps + `]}`
	if err := os.WriteFile(filepath.Join(filepath.Dir(cfg.StatePath), "applist.json"), []byte(cache), 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestRunGameByName(t *testing.T) {
	cfgPath := testConfig(t)
	seedAppListCache(t, cfgPath,
		`{"appid":896660,"name":"Valheim Dedicated Server"},{"appid":892970,"name":"Valheim"},{"appid":111,"name":"Valheim Plus"}`)

	// Unambiguous prefix, no terminal → resolved automatically.
	out, err := execRoot(t, "--config", cfgPath, "run",
		"--game", "valheim dedicated server", "--dir", filepath.Join(t.TempDir(), "x"), "--dry-run")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "app 896660") {
		t.Fatalf("game name not resolved to the app id:\n%s", out)
	}

	// Exact name match wins even among many candidates.
	out, err = execRoot(t, "--config", cfgPath, "run",
		"--game", "Valheim", "--dir", filepath.Join(t.TempDir(), "y"), "--dry-run")
	if err != nil || !strings.Contains(out, "app 892970") {
		t.Fatalf("exact name must auto-resolve: %v\n%s", err, out)
	}

	// Ambiguous (several candidates, none an exact match) without a
	// terminal → error advising --app.
	_, err = execRoot(t, "--config", cfgPath, "run",
		"--game", "valh", "--dir", "/tmp/x", "--dry-run")
	if err == nil || !strings.Contains(err.Error(), "--app") {
		t.Fatalf("ambiguous name must error with advice, got: %v", err)
	}

	// --app and --game together → rejected.
	_, err = execRoot(t, "--config", cfgPath, "run", "--app", "1", "--game", "x", "--dir", "/tmp/x")
	if err == nil {
		t.Fatal("--app + --game must be rejected")
	}
}

func TestRunRejectsMixedModes(t *testing.T) {
	_, err := execRoot(t, "--config", testConfig(t), "run", "sdk", "--app", "1007", "--dir", "/x")
	if err == nil {
		t.Fatal("profiles + explicit flags together must be rejected")
	}
	_, err = execRoot(t, "--config", testConfig(t), "run", "--save-as-profile", "x", "sdk")
	if err == nil {
		t.Fatal("--save-as-profile without explicit flags must be rejected")
	}
}
