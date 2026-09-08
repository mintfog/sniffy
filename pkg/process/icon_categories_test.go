// Copyright 2026 The mintfog Authors
// SPDX-License-Identifier: Apache-2.0
// Use of this source code is governed by an Apache 2.0
// license that can be found in the LICENSE file.

//go:build !windows

package process

import (
	"bytes"
	"encoding/base64"
	"image/color"
	"image/png"
	"os"
	"path/filepath"
	"testing"
)

func TestIconCategoryForPath(t *testing.T) {
	t.Parallel()

	tests := []struct {
		path string
		want string
	}{
		{path: "/usr/bin/Firefox", want: "browser"},
		{path: "/opt/JetBrains/GoLand", want: "development"},
		{path: "/usr/bin/zsh", want: "terminal"},
		{path: "/usr/local/bin/httpie", want: "api-tools"},
		{path: "/sbin/systemd", want: "system"},
		{path: "/usr/bin/mitmproxy", want: "networking"},
		{path: "/opt/acme/worker", want: "application"},
	}

	for _, tt := range tests {
		t.Run(tt.want, func(t *testing.T) {
			t.Parallel()
			if got := iconCategoryForPath(tt.path); got != tt.want {
				t.Fatalf("iconCategoryForPath(%q) = %q, want %q", tt.path, got, tt.want)
			}
		})
	}
}

func TestColorIconPNG(t *testing.T) {
	t.Parallel()

	encoded := colorIconPNG("sniffy")
	if encoded == "" {
		t.Fatal("colorIconPNG() returned empty data")
	}
	if encoded != colorIconPNG("sniffy") {
		t.Fatal("colorIconPNG() is not deterministic")
	}
	if encoded == colorIconPNG("firefox") {
		t.Fatal("different names unexpectedly produced identical icons")
	}

	data, err := base64.StdEncoding.DecodeString(encoded)
	if err != nil {
		t.Fatalf("decode base64 icon: %v", err)
	}
	img, err := png.Decode(bytes.NewReader(data))
	if err != nil {
		t.Fatalf("decode PNG icon: %v", err)
	}
	if got := img.Bounds().Size(); got.X != 32 || got.Y != 32 {
		t.Fatalf("icon size = %v, want 32x32", got)
	}
	if got := color.NRGBAModel.Convert(img.At(0, 0)).(color.NRGBA); got.A != 0 {
		t.Fatalf("corner alpha = %d, want 0", got.A)
	}
	if got := color.NRGBAModel.Convert(img.At(16, 16)).(color.NRGBA); got.A != 255 {
		t.Fatalf("center alpha = %d, want 255", got.A)
	}
}

func TestIconInfoFactories(t *testing.T) {
	t.Parallel()

	fallback := fallbackIcon("/usr/bin/firefox")
	if fallback.IconType != "png" || fallback.IconSize != "32x32" || fallback.HasIcon || fallback.IconCategory != "browser" {
		t.Fatalf("fallbackIcon() = %+v", fallback)
	}
	if _, err := base64.StdEncoding.DecodeString(fallback.IconData); err != nil {
		t.Fatalf("fallback icon data is not base64: %v", err)
	}

	defaultInfo := defaultIcon()
	if defaultInfo.IconType != "png" || defaultInfo.IconSize != "32x32" || defaultInfo.HasIcon || defaultInfo.IconCategory != "application" {
		t.Fatalf("defaultIcon() = %+v", defaultInfo)
	}

	encoded := encodeIconFile([]byte("icon payload"), "svg")
	if encoded.IconData != "aWNvbiBwYXlsb2Fk" || encoded.IconType != "svg" || encoded.IconSize != "" || !encoded.HasIcon {
		t.Fatalf("encodeIconFile() = %+v", encoded)
	}
}

func TestFileExistsAndReadCapped(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	regular := filepath.Join(dir, "icon.png")
	if err := os.WriteFile(regular, []byte("png"), 0o600); err != nil {
		t.Fatalf("write icon: %v", err)
	}
	if !fileExists(regular) {
		t.Fatal("fileExists() did not find a regular file")
	}
	if fileExists(dir) || fileExists(filepath.Join(dir, "missing")) {
		t.Fatal("fileExists() accepted a directory or missing path")
	}

	got, err := readCapped(regular)
	if err != nil {
		t.Fatalf("readCapped(): %v", err)
	}
	if !bytes.Equal(got, []byte("png")) {
		t.Fatalf("readCapped() = %q, want %q", got, "png")
	}

	atLimit := filepath.Join(dir, "at-limit")
	if err := os.WriteFile(atLimit, make([]byte, maxIconFileSize), 0o600); err != nil {
		t.Fatalf("write at-limit icon: %v", err)
	}
	if _, err := readCapped(atLimit); err != nil {
		t.Fatalf("readCapped() rejected file at limit: %v", err)
	}

	overLimit := filepath.Join(dir, "over-limit")
	f, err := os.Create(overLimit)
	if err != nil {
		t.Fatalf("create over-limit icon: %v", err)
	}
	if err := f.Truncate(maxIconFileSize + 1); err != nil {
		f.Close()
		t.Fatalf("truncate over-limit icon: %v", err)
	}
	if err := f.Close(); err != nil {
		t.Fatalf("close over-limit icon: %v", err)
	}
	if _, err := readCapped(overLimit); err == nil {
		t.Fatal("readCapped() accepted oversized file")
	}
	if _, err := readCapped(filepath.Join(dir, "missing")); err == nil {
		t.Fatal("readCapped() accepted missing file")
	}
}
