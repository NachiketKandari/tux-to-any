package main

import (
	"os"
	"path/filepath"
	"testing"

	"tux-to-any/internal/db"
)

const envTestYAML = `
run:
  profile: yaml-prof
models:
  - name: yaml-prof
    provider: openai-compatible
    model: yaml-model
    apiBase: http://127.0.0.1:9/v1
    apiKeyEnv: TUXGO_TEST_ENV_ONLY_KEY
    apiKey: yaml-literal-key
    requestOptions:
      timeout: 30s
database:
  dsnEnv: TUXGO_TEST_ENV_ONLY_DSN
  dsn: yaml-literal-dsn
`

func writeEnvTestYAML(t *testing.T, dir, name string) string {
	t.Helper()
	path := filepath.Join(dir, name)
	if err := os.WriteFile(path, []byte(envTestYAML), 0o644); err != nil {
		t.Fatal(err)
	}
	return path
}

// Explicit -config wins over both the local file and TUXGO_CONFIG.
func TestLoadRunConfigExplicitWins(t *testing.T) {
	dir := t.TempDir()
	t.Chdir(dir)
	if err := os.WriteFile(".tuxgo.yaml", []byte("run:\n  profile: local-prof\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	explicit := writeEnvTestYAML(t, t.TempDir(), "explicit.yaml")
	envPath := writeEnvTestYAML(t, t.TempDir(), "env.yaml")
	t.Setenv(configEnvVar, envPath)

	cfg, src, err := loadRunConfig(explicit)
	if err != nil {
		t.Fatalf("loadRunConfig explicit: %v", err)
	}
	if src != explicit {
		t.Errorf("source = %q, want explicit %q", src, explicit)
	}
	if cfg.Run.Profile != "yaml-prof" {
		t.Errorf("profile = %q, want yaml-prof", cfg.Run.Profile)
	}
}

// Local ./.tuxgo.yaml wins over TUXGO_CONFIG (a checkout's working copy
// beats the server-wide pointer).
func TestLoadRunConfigLocalBeatsEnv(t *testing.T) {
	dir := t.TempDir()
	t.Chdir(dir)
	local := `
run:
  profile: yaml-prof
models:
  - name: yaml-prof
    provider: openai-compatible
    model: m
    apiBase: http://127.0.0.1:9/v1
    apiKey: local-literal
    requestOptions:
      timeout: 30s
`
	if err := os.WriteFile(".tuxgo.yaml", []byte(local), 0o644); err != nil {
		t.Fatal(err)
	}
	envPath := writeEnvTestYAML(t, t.TempDir(), "env.yaml")
	t.Setenv(configEnvVar, envPath)

	cfg, src, err := loadRunConfig("")
	if err != nil {
		t.Fatalf("loadRunConfig: %v", err)
	}
	if src != defaultConfigFile {
		t.Errorf("source = %q, want %q", src, defaultConfigFile)
	}
	m, err := cfg.Route("")
	if err != nil {
		t.Fatal(err)
	}
	key, source, err := m.ResolveKey()
	if err != nil || key != "local-literal" || source != "literal" {
		t.Errorf("local key = %q %q %v", key, source, err)
	}
}

// TUXGO_CONFIG supplies the run config (yaml literals included) from a bare
// directory — the web job-tmpdir shape: no local .tuxgo.yaml present.
func TestLoadRunConfigEnvFallbackReadsYamlLiterals(t *testing.T) {
	dir := t.TempDir()
	t.Chdir(dir)
	envPath := writeEnvTestYAML(t, t.TempDir(), "server.yaml")
	t.Setenv(configEnvVar, envPath)
	os.Unsetenv("TUXGO_TEST_ENV_ONLY_KEY")
	os.Unsetenv("TUXGO_TEST_ENV_ONLY_DSN")

	cfg, src, err := loadRunConfig("")
	if err != nil {
		t.Fatalf("loadRunConfig via %s: %v", configEnvVar, err)
	}
	if src != envPath {
		t.Errorf("source = %q, want %q", src, envPath)
	}

	// API key: yaml literal resolves with no env var set ...
	m, err := cfg.Route("")
	if err != nil {
		t.Fatal(err)
	}
	if m.Name != "yaml-prof" {
		t.Fatalf("profile = %q, want yaml-prof", m.Name)
	}
	key, source, err := m.ResolveKey()
	if err != nil || key != "yaml-literal-key" || source != "literal" {
		t.Errorf("yaml literal key = %q %q %v", key, source, err)
	}

	// ... and the env var still wins when it appears.
	t.Setenv("TUXGO_TEST_ENV_ONLY_KEY", "env-key")
	key, source, err = m.ResolveKey()
	if err != nil || key != "env-key" || source != "env:TUXGO_TEST_ENV_ONLY_KEY" {
		t.Errorf("env precedence = %q %q %v", key, source, err)
	}

	// Database DSN: same env-first, literal-fallback contract.
	dcfg := db.Config{Driver: cfg.Database.Driver, DSN: cfg.Database.DSN, DSNEnv: cfg.Database.DSNEnv}
	if dsn, dsrc := dcfg.ResolveDSN(); dsn != "yaml-literal-dsn" || dsrc != "literal" {
		t.Errorf("yaml literal dsn = %q %q", dsn, dsrc)
	}
	t.Setenv("TUXGO_TEST_ENV_ONLY_DSN", "env-dsn")
	if dsn, dsrc := dcfg.ResolveDSN(); dsn != "env-dsn" || dsrc != "env:TUXGO_TEST_ENV_ONLY_DSN" {
		t.Errorf("env dsn precedence = %q %q", dsn, dsrc)
	}
}

// A TUXGO_CONFIG pointer at a missing file fails loudly — never a silent
// keyless run.
func TestLoadRunConfigEnvMissingIsError(t *testing.T) {
	t.Chdir(t.TempDir())
	t.Setenv(configEnvVar, filepath.Join("nope", "missing.yaml"))
	if _, _, err := loadRunConfig(""); err == nil {
		t.Error("missing TUXGO_CONFIG target must error")
	}
}

// No flag, no local file, no env: stock defaults, same as before.
func TestLoadRunConfigDefaultsWithoutEnv(t *testing.T) {
	t.Chdir(t.TempDir())
	os.Unsetenv(configEnvVar)
	cfg, src, err := loadRunConfig("")
	if err != nil {
		t.Fatalf("loadRunConfig: %v", err)
	}
	if src != "defaults" {
		t.Errorf("source = %q, want defaults", src)
	}
	if cfg.Run.Profile != "onprem-vllm" {
		t.Errorf("profile = %q, want onprem-vllm", cfg.Run.Profile)
	}
}
