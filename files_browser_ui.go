package main

import (
	"fmt"
	"path"
	"strings"

	"github.com/gdamore/tcell/v2"
	"github.com/rivo/tview"
)

// openChannelFiles opens the SharePoint Files tab browser for the given channel.
func (s *AppState) openChannelFiles(channelID string) {
	siteURL, rootFolder, title, ok := s.channelFilesContext(channelID)
	if !ok {
		s.showDownloadResult("Files unavailable", "A channel's Files tab is only available for team channels (not chats).")
		return
	}

	s.fileBrowser = &fileBrowserState{
		siteURL:    siteURL,
		host:       hostOf(siteURL),
		rootFolder: rootFolder,
		folder:     rootFolder,
		title:      title,
	}

	list := tview.NewList()
	list.SetBorder(true)
	list.SetTitleAlign(tview.AlignCenter)
	list.SetBackgroundColor(tcell.ColorBlack)
	list.SetInputCapture(func(event *tcell.EventKey) *tcell.EventKey {
		switch event.Key() {
		case tcell.KeyEscape, tcell.KeyLeft, tcell.KeyBackspace, tcell.KeyBackspace2:
			s.fileBrowserBack()
			return nil
		}
		return event
	})

	s.components[MoFileBrowser] = list
	s.previousFocus = s.focusedComponent
	s.pages.AddPage(PageFileBrowser, centeredModal(list, 90, 26), true, true)
	s.pages.SendToFront(PageFileBrowser)
	s.app.SetFocus(list)

	s.navigateFolder(rootFolder)
}

// navigateFolder lists folder asynchronously and renders the result.
func (s *AppState) navigateFolder(folder string) {
	fb := s.fileBrowser
	if fb == nil {
		return
	}
	fb.folder = folder

	list, ok := s.components[MoFileBrowser].(*tview.List)
	if !ok {
		return
	}
	list.Clear()
	list.SetTitle(s.fileBrowserTitle())
	list.AddItem("Loading...", folderDisplayPath(fb.rootFolder, folder), 0, nil)

	ctx := s.appContext()
	go func() {
		entries, err := s.listSharePointFolder(ctx, fb.host, fb.siteURL, folder)
		s.app.QueueUpdateDraw(func() {
			if s.fileBrowser != fb || fb.folder != folder {
				return // user navigated away while loading
			}
			s.renderFileBrowserEntries(entries, err)
		})
	}()
}

func (s *AppState) renderFileBrowserEntries(entries []spEntry, err error) {
	fb := s.fileBrowser
	list, ok := s.components[MoFileBrowser].(*tview.List)
	if fb == nil || !ok {
		return
	}

	list.Clear()
	list.SetTitle(s.fileBrowserTitle())

	if err != nil {
		list.AddItem("Failed to list folder", err.Error(), 0, nil)
		return
	}

	if fb.folder != fb.rootFolder {
		list.AddItem("[..]", "Go to parent folder", 0, func() { s.fileBrowserUp() })
	}

	if len(entries) == 0 {
		list.AddItem("(empty)", "No files or folders here.", 0, nil)
	}

	for _, entry := range entries {
		entry := entry
		if entry.IsFolder {
			list.AddItem("[DIR] "+entry.Name, "folder", 0, func() {
				s.navigateFolder(entry.ServerRelativeURL)
			})
		} else {
			list.AddItem(entry.Name, humanSize(entry.Size), 0, func() {
				s.downloadBrowserFile(entry)
			})
		}
	}
}

// fileBrowserBack goes up one folder, or closes the browser at the channel root.
func (s *AppState) fileBrowserBack() {
	fb := s.fileBrowser
	if fb == nil {
		return
	}
	if fb.folder == fb.rootFolder {
		s.dismissFileBrowser()
		return
	}
	s.fileBrowserUp()
}

func (s *AppState) fileBrowserUp() {
	fb := s.fileBrowser
	if fb == nil {
		return
	}
	parent := path.Dir(fb.folder)
	if !strings.HasPrefix(parent+"/", fb.rootFolder+"/") {
		parent = fb.rootFolder
	}
	s.navigateFolder(parent)
}

func (s *AppState) dismissFileBrowser() {
	s.pages.RemovePage(PageFileBrowser)
	delete(s.components, MoFileBrowser)
	s.fileBrowser = nil

	target := s.previousFocus
	if target == "" {
		target = TrChat
	}
	s.focusComponent(target)
}

// downloadBrowserFile saves a file from the channel folder in the background.
func (s *AppState) downloadBrowserFile(entry spEntry) {
	fb := s.fileBrowser
	if fb == nil {
		return
	}
	host, site := fb.host, fb.siteURL

	s.showDownloadProgress(fmt.Sprintf("Downloading\n\n%s", entry.Name))
	go func() {
		dest, err := s.downloadServerRelative(s.appContext(), host, site, entry.ServerRelativeURL, entry.Name)
		s.app.QueueUpdateDraw(func() {
			if err != nil {
				s.appLogger().WithError(err).WithField("file", entry.Name).Warn("channel file download failed")
				s.showDownloadResult("Download failed", err.Error())
				return
			}
			s.appLogger().WithField("path", dest).Info("channel file downloaded")
			s.showDownloadResult("Downloaded", dest)
		})
	}()
}

func (s *AppState) fileBrowserTitle() string {
	fb := s.fileBrowser
	if fb == nil {
		return "Files"
	}
	rel := folderDisplayPath(fb.rootFolder, fb.folder)
	return fmt.Sprintf("Files: %s%s", fb.title, rel)
}

// folderDisplayPath shows the path relative to the channel root, e.g. "/09/01".
func folderDisplayPath(root, folder string) string {
	rel := strings.TrimPrefix(folder, root)
	if rel == "" {
		return ""
	}
	return rel
}

func humanSize(size int64) string {
	switch {
	case size <= 0:
		return ""
	case size < 1024:
		return fmt.Sprintf("%d B", size)
	case size < 1024*1024:
		return fmt.Sprintf("%.1f KB", float64(size)/1024)
	default:
		return fmt.Sprintf("%.1f MB", float64(size)/(1024*1024))
	}
}
