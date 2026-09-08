// Copyright 2026 The mintfog Authors
// SPDX-License-Identifier: Apache-2.0
// Use of this source code is governed by an Apache 2.0
// license that can be found in the LICENSE file.

//go:build darwin

package process

import (
	"os"
	"path/filepath"
	"sync"
	"testing"
)

func TestFindAppBundle(t *testing.T) {
	t.Parallel()
	tests := []struct {
		path string
		want string
	}{
		{path: "/Applications/Client.app/Contents/MacOS/client", want: "/Applications/Client.app"},
		{path: "/Applications/Client.app", want: "/Applications/Client.app"},
		{path: "/usr/local/bin/client", want: ""},
		{path: "client", want: ""},
	}
	for _, tt := range tests {
		if got := findAppBundle(tt.path); got != tt.want {
			t.Errorf("findAppBundle(%q) = %q, want %q", tt.path, got, tt.want)
		}
	}
}

func TestLocateICNSFallback(t *testing.T) {
	t.Parallel()
	appDir := filepath.Join(t.TempDir(), "Client.app")
	resources := filepath.Join(appDir, "Contents", "Resources")
	if err := os.MkdirAll(resources, 0o700); err != nil {
		t.Fatalf("create Resources: %v", err)
	}
	iconPath := filepath.Join(resources, "Client.ICNS")
	if err := os.WriteFile(iconPath, []byte("icns"), 0o600); err != nil {
		t.Fatalf("write ICNS: %v", err)
	}
	if got := locateICNS(appDir); got != iconPath {
		t.Fatalf("locateICNS() = %q, want %q", got, iconPath)
	}
}

func TestDarwinIconFallbackAndCache(t *testing.T) {
	extractor := NewIconExtractor()
	first, err := extractor.ExtractIcon("/usr/local/bin/firefox")
	if err != nil {
		t.Fatalf("ExtractIcon(): %v", err)
	}
	if first.HasIcon || first.IconCategory != "browser" || first.IconData == "" {
		t.Fatalf("ExtractIcon() = %+v", first)
	}

	var wg sync.WaitGroup
	for range 32 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			got, extractErr := extractor.ExtractIcon("/usr/local/bin/firefox")
			if extractErr != nil {
				t.Errorf("concurrent ExtractIcon(): %v", extractErr)
				return
			}
			if got != first {
				t.Errorf("concurrent ExtractIcon() returned an unexpected entry")
			}
		}()
	}
	wg.Wait()

	empty, err := extractor.ExtractIcon("")
	if err != nil {
		t.Fatalf("ExtractIcon(empty): %v", err)
	}
	if empty.HasIcon || empty.IconCategory != "application" || empty.IconData == "" {
		t.Fatalf("ExtractIcon(empty) = %+v", empty)
	}
}
