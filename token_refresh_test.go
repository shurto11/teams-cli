package main

import (
	"os"
	"path/filepath"
	"testing"
)

func TestLocateTokenRefreshScriptInTokenDir(t *testing.T) {
	dir := t.TempDir()
	script := filepath.Join(dir, tokenRefreshScriptName)
	if err := os.WriteFile(script, []byte("#!/usr/bin/env bash\n"), 0o755); err != nil {
		t.Fatalf("unable to write script: %v", err)
	}

	got, ok := locateTokenRefreshScript(dir)
	if !ok {
		t.Fatal("expected to locate the refresh script")
	}
	if got != script {
		t.Fatalf("expected %q, got %q", script, got)
	}
}

func TestLocateTokenRefreshScriptMissing(t *testing.T) {
	dir := t.TempDir()

	if _, ok := locateTokenRefreshScript(dir); ok {
		t.Fatal("expected no script to be located in an empty directory")
	}
}

func TestLocateTokenRefreshScriptEnvOverride(t *testing.T) {
	dir := t.TempDir()
	override := filepath.Join(dir, "custom-refresh.sh")
	if err := os.WriteFile(override, []byte("#!/usr/bin/env bash\n"), 0o755); err != nil {
		t.Fatalf("unable to write override script: %v", err)
	}
	t.Setenv(tokenRefreshScriptEnv, override)

	got, ok := locateTokenRefreshScript("/nonexistent/token/dir")
	if !ok {
		t.Fatal("expected the override script to be located")
	}
	if got != override {
		t.Fatalf("expected %q, got %q", override, got)
	}
}

func TestLocateTokenRefreshScriptEnvOverrideMissing(t *testing.T) {
	t.Setenv(tokenRefreshScriptEnv, "/does/not/exist.sh")

	if _, ok := locateTokenRefreshScript(t.TempDir()); ok {
		t.Fatal("expected a missing override path to skip refresh")
	}
}

func TestEffectiveTokenDirExplicit(t *testing.T) {
	if got := effectiveTokenDir("/tmp/custom"); got != "/tmp/custom" {
		t.Fatalf("expected explicit token dir to be returned, got %q", got)
	}
}
