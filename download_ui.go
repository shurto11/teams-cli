package main

import (
	"fmt"

	"github.com/gdamore/tcell/v2"
	"github.com/rivo/tview"
)

// handleDownloadRequest is invoked when the user presses 'd' in the messages pane.
func (s *AppState) handleDownloadRequest() {
	chatList, ok := s.components[ViChat].(*tview.List)
	if !ok {
		return
	}

	message, ok := s.messageAt(chatList.GetCurrentItem())
	if !ok {
		s.showDownloadResult("No message selected", "Select a message with an attachment, then press d.")
		return
	}

	files := attachmentsForMessage(message)
	switch len(files) {
	case 0:
		s.showDownloadResult("No attachment", "This message has no downloadable SharePoint file.")
	case 1:
		s.startDownload(files[0])
	default:
		s.showDownloadPicker(files)
	}
}

// showDownloadPicker lists the attachments of a message so the user can choose one.
func (s *AppState) showDownloadPicker(files []teamsFile) {
	list := tview.NewList()
	list.SetBorder(true)
	list.SetTitle("Download attachment")
	list.SetTitleAlign(tview.AlignCenter)
	list.SetBackgroundColor(tcell.ColorBlack)

	for _, file := range files {
		file := file
		list.AddItem(file.Name, fileSecondaryText(file), 0, func() {
			s.dismissDownloadPicker()
			s.startDownload(file)
		})
	}

	list.SetInputCapture(func(event *tcell.EventKey) *tcell.EventKey {
		if event.Key() == tcell.KeyEscape {
			s.dismissDownloadPicker()
			return nil
		}
		return event
	})

	s.components[MoDownloadPicker] = list
	modal := centeredModal(list, 70, len(files)+2)

	s.previousFocus = s.focusedComponent
	s.pages.AddPage(PageDownloadPicker, modal, true, true)
	s.pages.SendToFront(PageDownloadPicker)
	s.app.SetFocus(list)
}

func (s *AppState) dismissDownloadPicker() {
	s.pages.RemovePage(PageDownloadPicker)
	delete(s.components, MoDownloadPicker)
	s.restoreFocusAfterOverlay()
}

// startDownload saves a single file in the background, updating the status modal.
func (s *AppState) startDownload(file teamsFile) {
	s.showDownloadProgress(fmt.Sprintf("Downloading\n\n%s", file.Name))

	go func() {
		path, err := s.downloadAttachment(s.appContext(), file)
		s.app.QueueUpdateDraw(func() {
			if err != nil {
				s.appLogger().WithError(err).WithField("file", file.Name).Warn("attachment download failed")
				s.showDownloadResult("Download failed", err.Error())
				return
			}
			s.appLogger().WithField("path", path).Info("attachment downloaded")
			s.showDownloadResult("Downloaded", path)
		})
	}()
}

// showDownloadProgress shows a non-dismissable status while a download runs.
func (s *AppState) showDownloadProgress(text string) {
	s.pages.RemovePage(PageDownloadStatus)

	view := tview.NewTextView()
	view.SetText(text)
	view.SetTextAlign(tview.AlignCenter)
	view.SetBorder(true)
	view.SetTitle("Please wait")
	view.SetTitleAlign(tview.AlignCenter)
	view.SetBackgroundColor(tcell.ColorBlack)

	s.components[MoDownloadStatus] = view
	if s.focusedComponent != "" && !s.downloadOverlayVisible() {
		s.previousFocus = s.focusedComponent
	}
	s.pages.AddPage(PageDownloadStatus, centeredModal(view, 70, 7), true, true)
	s.pages.SendToFront(PageDownloadStatus)
	s.app.SetFocus(view)
}

// showDownloadResult shows a dismissable modal with the outcome of a download.
func (s *AppState) showDownloadResult(title, detail string) {
	s.pages.RemovePage(PageDownloadStatus)

	modal := tview.NewModal()
	modal.SetText(title + "\n\n" + detail)
	modal.AddButtons([]string{"OK"})
	modal.SetDoneFunc(func(int, string) {
		s.dismissDownloadStatus()
	})

	s.components[MoDownloadStatus] = modal
	s.pages.AddPage(PageDownloadStatus, modal, true, true)
	s.pages.SendToFront(PageDownloadStatus)
	s.app.SetFocus(modal)
}

func (s *AppState) dismissDownloadStatus() {
	s.pages.RemovePage(PageDownloadStatus)
	delete(s.components, MoDownloadStatus)
	s.restoreFocusAfterOverlay()
}

func (s *AppState) downloadOverlayVisible() bool {
	name, _ := s.pages.GetFrontPage()
	return name == PageDownloadStatus || name == PageDownloadPicker
}

func (s *AppState) restoreFocusAfterOverlay() {
	target := s.previousFocus
	if target == "" {
		target = ViChat
	}
	s.focusComponent(target)
}

func fileSecondaryText(file teamsFile) string {
	if file.Type != "" {
		return "type: " + file.Type
	}
	return file.ObjectURL
}

// centeredModal wraps a primitive in a centered, fixed-size overlay.
func centeredModal(primitive tview.Primitive, width, height int) tview.Primitive {
	return tview.NewFlex().
		AddItem(nil, 0, 1, false).
		AddItem(tview.NewFlex().SetDirection(tview.FlexRow).
			AddItem(nil, 0, 1, false).
			AddItem(primitive, height, 1, true).
			AddItem(nil, 0, 1, false), width, 1, true).
		AddItem(nil, 0, 1, false)
}
