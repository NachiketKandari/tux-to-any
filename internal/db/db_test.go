package db

import (
	"context"
	"errors"
	"testing"
	"time"
)

func TestDisabledByDefault(t *testing.T) {
	cfg := DefaultConfig()
	cfg.DSNEnv = "TUXGO_TEST_ORACLE_DSN_UNSET"
	cfg.DSN = ""
	h, err := Open(cfg)
	if err != nil {
		t.Fatalf("disabled open must not error: %v", err)
	}
	if h.Enabled() {
		t.Error("handle must be disabled with no DSN")
	}
	if h.Source() != "none" {
		t.Errorf("source = %q, want none", h.Source())
	}
	if err := h.Ping(context.Background()); !errors.Is(err, ErrDisabled) {
		t.Errorf("ping on disabled = %v, want ErrDisabled", err)
	}
	if _, err := h.Query(context.Background(), "SELECT 1 FROM DUAL"); !errors.Is(err, ErrDisabled) {
		t.Errorf("query on disabled = %v, want ErrDisabled", err)
	}
	if _, err := h.Exec(context.Background(), "SELECT 1 FROM DUAL"); !errors.Is(err, ErrDisabled) {
		t.Errorf("exec on disabled = %v, want ErrDisabled", err)
	}
	if err := h.Close(); err != nil {
		t.Errorf("close on disabled = %v, want nil", err)
	}
	st := h.Status()
	if st["enabled"] != "false" {
		t.Errorf("status = %v, want enabled=false", st)
	}
}

func TestResolveDSNPrecedence(t *testing.T) {
	t.Setenv("TUXGO_TEST_ORACLE_DSN", "env-dsn")
	cfg := DefaultConfig()
	cfg.DSNEnv = "TUXGO_TEST_ORACLE_DSN"
	cfg.DSN = "literal-dsn"
	if dsn, src := cfg.ResolveDSN(); dsn != "env-dsn" || src != "env:TUXGO_TEST_ORACLE_DSN" {
		t.Errorf("env precedence = %q %q", dsn, src)
	}
}

func TestUnregisteredDriverFailsLoudly(t *testing.T) {
	cfg := DefaultConfig()
	cfg.Driver = "oracle-typo-driver"
	cfg.DSNEnv = ""
	cfg.DSN = "user/pw@host:1521/svc"
	if _, err := Open(cfg); err == nil {
		t.Error("unregistered driver must fail, not silently disable")
	}
}

func TestDisabledStatusShape(t *testing.T) {
	h, _ := Open(Config{DSNEnv: "TUXGO_TEST_ORACLE_DSN_UNSET_XYZ"})
	_ = time.Second
	if got := h.Status()["source"]; got != "none" {
		t.Errorf("source = %q, want none", got)
	}
}
