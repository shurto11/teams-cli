package main

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
)

// spEntry is a file or folder inside a SharePoint document library.
type spEntry struct {
	Name              string
	ServerRelativeURL string
	IsFolder          bool
	Size              int64
}

// fileBrowserState tracks the channel Files tab the user is browsing.
type fileBrowserState struct {
	siteURL    string // e.g. https://scii.sharepoint.com/sites/2026-2
	host       string // e.g. scii.sharepoint.com
	rootFolder string // channel folder, the browser cannot go above this
	folder     string // folder currently shown
	title      string // channel display name

	rowEntries []*spEntry // aligned with list rows; nil for ".."/placeholder rows
}

// channelFilesContext resolves the SharePoint site and folder backing a channel's
// Files tab from the cached conversation data.
func (s *AppState) channelFilesContext(channelID string) (siteURL, rootFolder, title string, ok bool) {
	channel, found := s.channelById[channelID]
	if !found || channel.Channel == nil || channel.parent == nil {
		return "", "", "", false
	}

	siteURL = strings.TrimRight(channel.parent.TeamSiteInformation.SharepointSiteUrl, "/")
	rootFolder = channel.DefaultFileSettings.FilesRelativePath
	if siteURL == "" || rootFolder == "" {
		return "", "", "", false
	}

	return siteURL, rootFolder, channel.DisplayName, true
}

// listSharePointFolder returns the folders and files directly under folder.
func (s *AppState) listSharePointFolder(ctx context.Context, host, siteURL, folder string) ([]spEntry, error) {
	token, err := s.sharePointToken(ctx, host)
	if err != nil {
		return nil, err
	}
	return s.listFolderWithToken(ctx, token, siteURL, folder)
}

// listFolderWithToken lists a folder using an already-minted SharePoint token.
func (s *AppState) listFolderWithToken(ctx context.Context, token, siteURL, folder string) ([]spEntry, error) {
	folders, err := s.fetchFolderChildren(ctx, token, siteURL, folder, true)
	if err != nil {
		return nil, err
	}
	files, err := s.fetchFolderChildren(ctx, token, siteURL, folder, false)
	if err != nil {
		return nil, err
	}

	entries := append(folders, files...)
	sort.SliceStable(entries, func(i, j int) bool {
		if entries[i].IsFolder != entries[j].IsFolder {
			return entries[i].IsFolder
		}
		return strings.ToLower(entries[i].Name) < strings.ToLower(entries[j].Name)
	})
	return entries, nil
}

// downloadFolderTree recursively downloads every file under folder into destDir,
// recreating the subfolder structure. progress is called after each file with the
// running count and the file just saved. Returns the number of files downloaded.
func (s *AppState) downloadFolderTree(ctx context.Context, token, siteURL, folder, destDir string, count int, progress func(done int, name string)) (int, error) {
	entries, err := s.listFolderWithToken(ctx, token, siteURL, folder)
	if err != nil {
		return count, err
	}

	for _, entry := range entries {
		if err := ctx.Err(); err != nil {
			return count, err
		}

		if entry.IsFolder {
			sub := filepath.Join(destDir, sanitizeFileName(entry.Name))
			count, err = s.downloadFolderTree(ctx, token, siteURL, entry.ServerRelativeURL, sub, count, progress)
			if err != nil {
				return count, err
			}
			continue
		}

		if _, err := s.downloadServerRelativeWithToken(ctx, token, siteURL, entry.ServerRelativeURL, destDir, entry.Name); err != nil {
			return count, fmt.Errorf("%s: %v", entry.Name, err)
		}
		count++
		if progress != nil {
			progress(count, entry.Name)
		}
	}

	return count, nil
}

func (s *AppState) fetchFolderChildren(ctx context.Context, token, siteURL, folder string, asFolders bool) ([]spEntry, error) {
	kind := "Files"
	selectFields := "Name,ServerRelativeUrl,Length"
	if asFolders {
		kind = "Folders"
		selectFields = "Name,ServerRelativeUrl,ItemCount"
	}

	endpoint := fmt.Sprintf("%s/_api/web/GetFolderByServerRelativeUrl('%s')/%s?$select=%s",
		strings.TrimRight(siteURL, "/"),
		encodeServerRelative(folder),
		kind,
		url.QueryEscape(selectFields),
	)

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Accept", "application/json;odata=nometadata")

	resp, err := s.downloadHTTPClient().Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 512))
		return nil, fmt.Errorf("SharePoint returned %s: %s", resp.Status, strings.TrimSpace(string(body)))
	}

	var payload struct {
		Value []struct {
			Name              string `json:"Name"`
			ServerRelativeURL string `json:"ServerRelativeUrl"`
			Length            string `json:"Length"`
		} `json:"value"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&payload); err != nil {
		return nil, fmt.Errorf("unable to decode folder listing: %v", err)
	}

	entries := make([]spEntry, 0, len(payload.Value))
	for _, item := range payload.Value {
		if asFolders && item.Name == "Forms" {
			continue // SharePoint system folder
		}
		size, _ := strconv.ParseInt(item.Length, 10, 64)
		entries = append(entries, spEntry{
			Name:              item.Name,
			ServerRelativeURL: item.ServerRelativeURL,
			IsFolder:          asFolders,
			Size:              size,
		})
	}
	return entries, nil
}

// encodeServerRelative percent-encodes each path segment while keeping the slashes,
// matching what SharePoint expects inside GetFolderByServerRelativeUrl('...').
func encodeServerRelative(path string) string {
	path = strings.ReplaceAll(path, "'", "''")
	segments := strings.Split(path, "/")
	for i, segment := range segments {
		segments[i] = url.PathEscape(segment)
	}
	return strings.Join(segments, "/")
}
