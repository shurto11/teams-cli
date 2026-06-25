package main

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/fossteams/teams-api/pkg/csa"
)

// realFilesJSON mirrors a real ChatMessage.Properties.Files payload: one SharePoint
// file plus one tab deeplink (which must be filtered out).
const realFilesJSON = `[` +
	`{"fileName":"ex9.zip","fileType":"zip","objectUrl":"https://scii.sharepoint.com/sites/2026-2/Shared%20Documents/00_a/ex9.zip","baseUrl":"https://scii.sharepoint.com/sites/2026-2","fileInfo":{"siteUrl":"https://scii.sharepoint.com/sites/2026-2"}},` +
	`{"fileName":"%E8%B3%87%E6%96%99","fileType":"deeplink","objectUrl":"","fileInfo":{"siteUrl":""}}` +
	`]`

func TestAttachmentsForMessage(t *testing.T) {
	msg := csa.ChatMessage{}
	msg.Properties.Files = realFilesJSON

	files := attachmentsForMessage(msg)
	if len(files) != 1 {
		t.Fatalf("expected 1 downloadable file, got %d", len(files))
	}
	f := files[0]
	if f.Name != "ex9.zip" {
		t.Errorf("unexpected name: %q", f.Name)
	}
	if f.Type != "zip" {
		t.Errorf("unexpected type: %q", f.Type)
	}
	if f.SiteURL != "https://scii.sharepoint.com/sites/2026-2" {
		t.Errorf("unexpected siteURL: %q", f.SiteURL)
	}
}

func TestAttachmentsForMessageEmpty(t *testing.T) {
	for _, raw := range []string{"", "[]", "not json"} {
		msg := csa.ChatMessage{}
		msg.Properties.Files = raw
		if got := attachmentsForMessage(msg); got != nil {
			t.Errorf("expected nil for %q, got %v", raw, got)
		}
	}
}

func TestDecodeFileName(t *testing.T) {
	if got := decodeFileName("%E8%B3%87%E6%96%99.pdf"); got != "資料.pdf" {
		t.Errorf("decode failed: %q", got)
	}
	if got := decodeFileName("plain.txt"); got != "plain.txt" {
		t.Errorf("plain name changed: %q", got)
	}
}

// TestDownloadIntegration downloads a real SharePoint file end-to-end. It is gated by
// TEAMS_CLI_IT=1 and requires a valid refresh token in ~/.config/fossteams.
// Set TEAMS_CLI_IT_OBJECT / TEAMS_CLI_IT_SITE to point at a file you can access.
func TestDownloadIntegration(t *testing.T) {
	if os.Getenv("TEAMS_CLI_IT") != "1" {
		t.Skip("set TEAMS_CLI_IT=1 to run the integration download test")
	}

	object := os.Getenv("TEAMS_CLI_IT_OBJECT")
	site := os.Getenv("TEAMS_CLI_IT_SITE")
	if object == "" {
		t.Fatal("TEAMS_CLI_IT_OBJECT must be set")
	}

	dir := t.TempDir()
	s := &AppState{downloadDir: dir}

	path, err := s.downloadAttachment(context.Background(), teamsFile{
		Name:      filepath.Base(object),
		ObjectURL: object,
		SiteURL:   site,
	})
	if err != nil {
		t.Fatalf("download failed: %v", err)
	}

	info, err := os.Stat(path)
	if err != nil {
		t.Fatalf("downloaded file missing: %v", err)
	}
	if info.Size() == 0 {
		t.Fatalf("downloaded file is empty")
	}
	if !strings.HasPrefix(path, dir) {
		t.Errorf("file saved outside temp dir: %s", path)
	}
	t.Logf("downloaded %d bytes to %s", info.Size(), path)
}
