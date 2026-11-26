// Copyright 2026 The mintfog Authors
// SPDX-License-Identifier: Apache-2.0

package main

import (
	"bytes"
	"os"
	"path/filepath"
	"testing"

	"github.com/mintfog/sniffy/internal/release"
	"github.com/stretchr/testify/require"
)

func TestVersionOutput(t *testing.T) {
	for _, test := range []struct{ value, output string }{
		{"v1.2.3+build-linux", "version=1.2.3+build-linux\nprerelease=false\n"},
		{"1.2.3-beta.1", "version=1.2.3-beta.1\nprerelease=true\n"},
	} {
		var stdout, stderr bytes.Buffer
		require.NoError(t, run(t.Context(), []string{"version", test.value}, &stdout, &stderr))
		require.Equal(t, test.output, stdout.String())
	}
}

func TestManifestCommandPreservesPreviousOutputOnFailure(t *testing.T) {
	directory := t.TempDir()
	artifact := filepath.Join(directory, "sniffy-linux-amd64")
	require.NoError(t, os.WriteFile(artifact, []byte("制品"), 0600))
	output := filepath.Join(directory, "release.json")
	checksums := filepath.Join(directory, "SHA256SUMS")
	notes := "## 问题修复\n\n- 修复 [Issue #3](https://github.com/mintfog/sniffy/issues/3)"
	args := []string{
		"manifest",
		"--version", "v1.2.3",
		"--dir", directory,
		"--out", output,
		"--checksums", checksums,
		"--published", "2026-09-22",
		"--notes-content", notes,
	}
	var stdout, stderr bytes.Buffer
	require.NoError(t, run(t.Context(), args, &stdout, &stderr))
	before, err := os.ReadFile(output)
	require.NoError(t, err)
	manifest, err := release.ParseManifest(before)
	require.NoError(t, err)
	require.Equal(t, notes, manifest.Notes)
	sums, err := os.ReadFile(checksums)
	require.NoError(t, err)
	require.Equal(t, release.Checksums(manifest), sums)
	require.NoError(t, os.Remove(artifact))
	require.Error(t, run(t.Context(), args, &stdout, &stderr))
	after, err := os.ReadFile(output)
	require.NoError(t, err)
	require.Equal(t, before, after)
}

func TestInvalidArguments(t *testing.T) {
	for _, args := range [][]string{
		nil,
		{"unknown"},
		{"version"},
		{"version", "1.2"},
		{"manifest", "--unknown"},
		{"manifest", "extra"},
		{"stage", "--timeout", "0"},
		{"promote", "extra"},
		{"wintun", "--arch", "amd64"},
		{"wintun", "--out", "wintun.dll"},
		{"wintun", "--arch", "amd64", "--out", "wintun.dll", "extra"},
		{"portable", "--arch", "amd64", "--binary", "sniffy.exe", "--name", "sniffy.exe"},
		{"portable", "--arch", "amd64", "--binary", "sniffy.exe", "--out", "sniffy.zip"},
	} {
		var stdout, stderr bytes.Buffer
		require.Error(t, run(t.Context(), args, &stdout, &stderr), args)
	}
}

func TestPortableRefusesToOverwriteBinary(t *testing.T) {
	binary := filepath.Join(t.TempDir(), "sniffy.exe")
	require.NoError(t, os.WriteFile(binary, []byte("exe"), 0600))
	var stdout, stderr bytes.Buffer
	err := run(t.Context(), []string{"portable", "--arch", "amd64", "--binary", binary, "--name", "sniffy.exe", "--out", binary}, &stdout, &stderr)
	require.ErrorContains(t, err, "不能覆盖")
	data, err := os.ReadFile(binary)
	require.NoError(t, err)
	require.Equal(t, "exe", string(data))
}
