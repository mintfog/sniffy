// Copyright 2026 The mintfog Authors
// SPDX-License-Identifier: Apache-2.0
// Use of this source code is governed by an Apache 2.0
// license that can be found in the LICENSE file.

//go:build windows

package process

import (
	"encoding/base64"
	"sync"
	"testing"
)

func TestWindowsFallbackIcons(t *testing.T) {
	t.Parallel()
	extractor := NewIconExtractor()

	tests := []struct {
		name     string
		category string
	}{
		{name: "chrome.exe", category: "browser"},
		{name: "code.exe", category: "development"},
		{name: "powershell.exe", category: "terminal"},
		{name: "postman.exe", category: "api-tools"},
		{name: "svchost.exe", category: "system"},
		{name: "wireshark.exe", category: "networking"},
		{name: "worker.exe", category: "application"},
	}
	for _, tt := range tests {
		got := extractor.getIconByFileName(tt.name)
		if got.IconType != "png" || got.IconSize != "32x32" || !got.HasIcon || got.IconCategory != tt.category {
			t.Errorf("getIconByFileName(%q) = %+v", tt.name, got)
		}
		if _, err := base64.StdEncoding.DecodeString(got.IconData); err != nil {
			t.Errorf("getIconByFileName(%q) returned invalid base64: %v", tt.name, err)
		}
	}

	defaultInfo, err := extractor.ExtractIcon("")
	if err != nil {
		t.Fatalf("ExtractIcon(empty): %v", err)
	}
	if defaultInfo.HasIcon || defaultInfo.IconCategory != "application" || defaultInfo.IconData == "" {
		t.Fatalf("ExtractIcon(empty) = %+v", defaultInfo)
	}
}

func TestWindowsIconCacheConcurrentAccess(t *testing.T) {
	extractor := NewIconExtractor()
	want := extractor.getIconByFileName("client.exe")

	var wg sync.WaitGroup
	for range 32 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			extractor.cacheIcon("client.exe", want)
			if got := extractor.getCachedIcon("client.exe"); got != want {
				t.Errorf("getCachedIcon() = %p, want %p", got, want)
			}
		}()
	}
	wg.Wait()
}
