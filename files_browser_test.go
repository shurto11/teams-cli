package main

import "testing"

func TestEncodeServerRelative(t *testing.T) {
	got := encodeServerRelative("/sites/2026-2/Shared Documents/00_講義資料")
	want := "/sites/2026-2/Shared%20Documents/00_%E8%AC%9B%E7%BE%A9%E8%B3%87%E6%96%99"
	if got != want {
		t.Errorf("encodeServerRelative = %q, want %q", got, want)
	}
	// slashes are preserved, single quotes are doubled then percent-encoded
	if got := encodeServerRelative("/a/b'c"); got != "/a/b%27%27c" {
		t.Errorf("quote handling = %q", got)
	}
}

func TestFolderDisplayPath(t *testing.T) {
	root := "/sites/x/Shared Documents/00"
	if got := folderDisplayPath(root, root); got != "" {
		t.Errorf("root should be empty, got %q", got)
	}
	if got := folderDisplayPath(root, root+"/01/02"); got != "/01/02" {
		t.Errorf("relative path = %q", got)
	}
}

func TestHumanSize(t *testing.T) {
	cases := map[int64]string{
		0:       "",
		512:     "512 B",
		2048:    "2.0 KB",
		5242880: "5.0 MB",
	}
	for size, want := range cases {
		if got := humanSize(size); got != want {
			t.Errorf("humanSize(%d) = %q, want %q", size, got, want)
		}
	}
}
