package main

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
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
