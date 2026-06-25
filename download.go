package main

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/fossteams/teams-api/pkg/csa"
)

// downloadClientID is the public Microsoft Teams client used by teams-refresh.sh.
// Combined with the refresh token in <token-dir>/refresh_token it can mint
// SharePoint Online access tokens on demand.
const downloadClientID = "1fec8e78-bce4-4aaf-ab1b-5451cc387264"

const downloadRequestTimeout = 60 * time.Second

// teamsFile is a downloadable SharePoint/OneDrive attachment extracted from a message.
type teamsFile struct {
	Name      string
	Type      string
	ObjectURL string
	SiteURL   string
}

// rawTeamsFile mirrors the JSON objects stored in ChatMessage.Properties.Files.
type rawTeamsFile struct {
	FileName  string `json:"fileName"`
	Title     string `json:"title"`
	FileType  string `json:"fileType"`
	Type      string `json:"type"`
	ObjectURL string `json:"objectUrl"`
	BaseURL   string `json:"baseUrl"`
	FileInfo  struct {
		FileURL string `json:"fileUrl"`
		SiteURL string `json:"siteUrl"`
	} `json:"fileInfo"`
}

// attachmentsForMessage parses Properties.Files and returns the SharePoint-hosted
// files that can be downloaded. Tab deeplinks and non-SharePoint entries are skipped.
func attachmentsForMessage(message csa.ChatMessage) []teamsFile {
	raw := strings.TrimSpace(message.Properties.Files)
	if raw == "" || raw == "[]" {
		return nil
	}

	var entries []rawTeamsFile
	if err := json.Unmarshal([]byte(raw), &entries); err != nil {
		return nil
	}

	var files []teamsFile
	for _, entry := range entries {
		fileType := firstNonEmpty(entry.FileType, entry.Type)
		if strings.EqualFold(fileType, "deeplink") {
			continue
		}

		objectURL := firstNonEmpty(entry.ObjectURL, entry.FileInfo.FileURL)
		if objectURL == "" {
			continue
		}
		if !strings.Contains(strings.ToLower(hostOf(objectURL)), "sharepoint.com") {
			continue
		}

		files = append(files, teamsFile{
			Name:      decodeFileName(firstNonEmpty(entry.FileName, entry.Title)),
			Type:      fileType,
			ObjectURL: objectURL,
			SiteURL:   firstNonEmpty(entry.FileInfo.SiteURL, entry.BaseURL),
		})
	}

	return files
}

// downloadAttachment fetches a SharePoint file to the configured download directory
// and returns the path it was written to.
func (s *AppState) downloadAttachment(ctx context.Context, file teamsFile) (string, error) {
	parsed, err := url.Parse(file.ObjectURL)
	if err != nil {
		return "", fmt.Errorf("invalid file URL: %v", err)
	}

	serverRelative := parsed.Path // url.Parse already percent-decodes the path
	if serverRelative == "" {
		return "", fmt.Errorf("file URL has no path")
	}

	site := strings.TrimRight(file.SiteURL, "/")
	if site == "" {
		site = parsed.Scheme + "://" + parsed.Host
	}

	token, err := s.sharePointToken(ctx, parsed.Host)
	if err != nil {
		return "", err
	}

	downloadURL := site + "/_layouts/15/download.aspx?SourceUrl=" + url.QueryEscape(serverRelative)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, downloadURL, nil)
	if err != nil {
		return "", err
	}
	req.Header.Set("Authorization", "Bearer "+token)

	resp, err := s.downloadHTTPClient().Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 512))
		return "", fmt.Errorf("SharePoint returned %s: %s", resp.Status, strings.TrimSpace(string(body)))
	}

	if err := os.MkdirAll(s.resolveDownloadDir(), 0o755); err != nil {
		return "", fmt.Errorf("unable to create download directory: %v", err)
	}

	name := sanitizeFileName(file.Name)
	if name == "" {
		name = sanitizeFileName(filepath.Base(serverRelative))
	}
	destPath := uniquePath(filepath.Join(s.resolveDownloadDir(), name))

	out, err := os.Create(destPath)
	if err != nil {
		return "", fmt.Errorf("unable to create file: %v", err)
	}
	defer out.Close()

	if _, err := io.Copy(out, resp.Body); err != nil {
		return "", fmt.Errorf("unable to write file: %v", err)
	}

	return destPath, nil
}

// sharePointToken mints a SharePoint Online access token for the given host using
// the stored refresh token. The rotated refresh token is written back best-effort.
func (s *AppState) sharePointToken(ctx context.Context, host string) (string, error) {
	rtPath := filepath.Join(s.resolveTokenDir(), "refresh_token")
	rtBytes, err := os.ReadFile(rtPath)
	if err != nil {
		return "", fmt.Errorf("unable to read refresh token (%s): run teams-refresh.sh login", rtPath)
	}
	refreshToken := strings.TrimSpace(string(rtBytes))
	if refreshToken == "" {
		return "", fmt.Errorf("refresh token is empty: run teams-refresh.sh login")
	}

	form := url.Values{}
	form.Set("grant_type", "refresh_token")
	form.Set("client_id", downloadClientID)
	form.Set("refresh_token", refreshToken)
	form.Set("resource", "https://"+host)

	endpoint := "https://login.microsoftonline.com/" + s.resolveTenant() + "/oauth2/token"
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, strings.NewReader(form.Encode()))
	if err != nil {
		return "", err
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")

	resp, err := s.downloadHTTPClient().Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()

	var payload struct {
		AccessToken      string `json:"access_token"`
		RefreshToken     string `json:"refresh_token"`
		Error            string `json:"error"`
		ErrorDescription string `json:"error_description"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&payload); err != nil {
		return "", fmt.Errorf("unable to decode token response: %v", err)
	}
	if payload.AccessToken == "" {
		detail := firstNonEmpty(payload.ErrorDescription, payload.Error, resp.Status)
		return "", fmt.Errorf("unable to get SharePoint token: %s", strings.SplitN(detail, "\n", 2)[0])
	}

	if payload.RefreshToken != "" && payload.RefreshToken != refreshToken {
		writeRefreshTokenAtomic(rtPath, payload.RefreshToken)
	}

	return payload.AccessToken, nil
}

func (s *AppState) downloadHTTPClient() *http.Client {
	return &http.Client{Timeout: downloadRequestTimeout}
}

func (s *AppState) resolveDownloadDir() string {
	if strings.TrimSpace(s.downloadDir) != "" {
		return s.downloadDir
	}
	if home, err := os.UserHomeDir(); err == nil {
		return filepath.Join(home, "ssd", "tui", "teams-cli", "downloads")
	}
	return "downloads"
}

func (s *AppState) resolveTokenDir() string {
	if strings.TrimSpace(s.tokenDir) != "" {
		return s.tokenDir
	}
	if dir, err := defaultTokenDir(); err == nil {
		return dir
	}
	return ""
}

// resolveTenant reads the tid claim from the skype token so SharePoint tokens are
// requested against the user's home tenant. Falls back to "organizations".
func (s *AppState) resolveTenant() string {
	tokenPath := tokenFilePath(s.resolveTokenDir(), tokenTypeSkype)
	if data, err := os.ReadFile(tokenPath); err == nil {
		if meta, err := parseJWTMetadata(string(data)); err == nil {
			if tid, ok := meta.Claims["tid"].(string); ok && strings.TrimSpace(tid) != "" {
				return strings.TrimSpace(tid)
			}
		}
	}
	return "organizations"
}

func writeRefreshTokenAtomic(path, value string) {
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, []byte(value), 0o600); err != nil {
		return
	}
	_ = os.Rename(tmp, path)
}

func hostOf(rawURL string) string {
	parsed, err := url.Parse(rawURL)
	if err != nil {
		return ""
	}
	return parsed.Host
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return value
		}
	}
	return ""
}

// decodeFileName percent-decodes names (Teams stores them URL-encoded). Names that
// are not encoded are returned unchanged.
func decodeFileName(name string) string {
	if decoded, err := url.PathUnescape(name); err == nil {
		return decoded
	}
	return name
}

func sanitizeFileName(name string) string {
	name = strings.TrimSpace(name)
	name = strings.ReplaceAll(name, "/", "_")
	name = strings.ReplaceAll(name, "\\", "_")
	name = strings.TrimLeft(name, ".")
	return name
}

// uniquePath appends " (n)" before the extension when the target already exists.
func uniquePath(path string) string {
	if _, err := os.Stat(path); os.IsNotExist(err) {
		return path
	}

	dir := filepath.Dir(path)
	base := filepath.Base(path)
	ext := filepath.Ext(base)
	stem := strings.TrimSuffix(base, ext)

	for i := 1; ; i++ {
		candidate := filepath.Join(dir, fmt.Sprintf("%s (%d)%s", stem, i, ext))
		if _, err := os.Stat(candidate); os.IsNotExist(err) {
			return candidate
		}
	}
}
