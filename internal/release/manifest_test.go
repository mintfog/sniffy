// Copyright 2026 The mintfog Authors
// SPDX-License-Identifier: Apache-2.0

package release

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/mintfog/sniffy/internal/update"
	"github.com/stretchr/testify/require"
	"golang.org/x/mod/semver"
)

func artifactFixture(t *testing.T, version string) (string, update.Manifest) {
	t.Helper()
	directory := t.TempDir()
	for _, name := range RequiredArtifacts() {
		require.NoError(t, os.WriteFile(filepath.Join(directory, name), []byte("测试制品："+name), 0600))
	}
	manifest, err := GenerateManifest(ManifestOptions{
		Directory:       directory,
		Version:         version,
		PublishedAt:     "2026-09-22",
		RequireComplete: true,
	})
	require.NoError(t, err)
	return directory, manifest
}

func TestManifestContract(t *testing.T) {
	directory, manifest := artifactFixture(t, "v1.2.3")
	require.Equal(t, "1.2.3", manifest.Version)
	require.Len(t, manifest.Assets, 14)
	require.NoError(t, manifest.Validate())
	for _, asset := range manifest.Assets {
		data, err := os.ReadFile(filepath.Join(directory, asset.Name))
		require.NoError(t, err)
		sum := sha256.Sum256(data)
		require.Equal(t, int64(len(data)), asset.Size)
		require.Equal(t, hex.EncodeToString(sum[:]), asset.SHA256)
		require.Equal(t, "https://cdn.gosniffy.com/releases/v1.2.3/"+asset.Name, asset.URL)
	}
	asset, found := manifest.AssetFor("windows", "amd64", "desktop")
	require.True(t, found)
	require.Equal(t, update.KindInstaller, asset.Kind)
	manifest.NotesURL = `https://example.com/?q="中文"&x=<note>`
	data, err := EncodeManifest(manifest)
	require.NoError(t, err)
	decoded, err := ParseManifest(data)
	require.NoError(t, err)
	require.Equal(t, manifest, decoded)
	require.Contains(t, string(data), "&x=<note>")
	var clientManifest update.Manifest
	require.NoError(t, json.Unmarshal(data, &clientManifest))
	require.NoError(t, clientManifest.Validate())
}

func TestGenerateManifestRejectsInvalidArtifacts(t *testing.T) {
	for _, name := range []string{"缺失制品", "空文件", "目录", "错误扩展名"} {
		t.Run(name, func(t *testing.T) {
			directory, _ := artifactFixture(t, "1.2.3")
			path := filepath.Join(directory, RequiredArtifacts()[0])
			require.NoError(t, os.Remove(path))
			switch name {
			case "空文件":
				require.NoError(t, os.WriteFile(path, nil, 0600))
			case "目录":
				require.NoError(t, os.Mkdir(path, 0700))
			case "错误扩展名":
				require.NoError(t, os.WriteFile(path+".exe", []byte("错误"), 0600))
			}
			_, err := GenerateManifest(ManifestOptions{Directory: directory, Version: "1.2.3", RequireComplete: true})
			require.Error(t, err)
		})
	}
	_, err := GenerateManifest(ManifestOptions{Directory: t.TempDir(), Version: "1.2.3"})
	require.Error(t, err)
}

func TestGenerateManifestUsesFilesAndStableOrder(t *testing.T) {
	directory, expected := artifactFixture(t, "1.2.3")
	require.NoError(t, os.WriteFile(filepath.Join(directory, "SHA256SUMS"), []byte("过期的校验和"), 0600))
	require.NoError(t, os.WriteFile(filepath.Join(directory, "README.md"), []byte("说明"), 0600))
	actual, err := GenerateManifest(ManifestOptions{Directory: directory, Version: "v1.2.3", PublishedAt: expected.PublishedAt})
	require.NoError(t, err)
	require.Equal(t, expected, actual)
	for _, base := range []string{"http://example.com", "https://user:pass@example.com", "https://example.com?", "https://example.com/#fragment"} {
		_, err := GenerateManifest(ManifestOptions{Directory: directory, Version: "1.2.3", BaseURL: base})
		require.Error(t, err, base)
	}
}

func TestStrictVersions(t *testing.T) {
	for _, value := range []string{"", "1", "1.2", "1.2.3.4", "01.2.3", "1.2.3-01", "1.2.3-a..b", "1.2.3+", "../bad", " 1.2.3"} {
		_, err := ParseVersion(value)
		require.Error(t, err, value)
	}
	ordered := []string{
		"1.2.3-1",
		"1.2.3-2",
		"1.2.3-9007199254740992",
		"1.2.3-9007199254740993",
		"1.2.3-92233720368547758080",
		"1.2.3-alpha",
		"1.2.3-alpha.1",
		"1.2.3-beta.2",
		"1.2.3-beta.10",
		"1.2.3-rc.1",
		"1.2.3",
		"1.3.0",
		"2.0.0",
	}
	for index, value := range ordered {
		version, err := ParseVersion("v" + value)
		require.NoError(t, err)
		require.Equal(t, value, version)
		if index > 0 {
			require.Equal(t, 1, semver.Compare("v"+version, "v"+ordered[index-1]))
		}
	}
	require.Equal(t, 0, semver.Compare("v1.2.3+build.1", "v1.2.3+build.2"))
	require.Empty(t, semver.Prerelease("v1.2.3+build-linux"))
	require.Equal(t, "-beta.1", semver.Prerelease("v1.2.3-beta.1+build-linux"))
}

func TestManifestValidation(t *testing.T) {
	_, manifest := artifactFixture(t, "1.2.3")
	for name, change := range map[string]func(*update.Manifest){
		"空制品":  func(m *update.Manifest) { m.Assets = nil },
		"非法版本": func(m *update.Manifest) { m.Version = "1.2" },
		"重复文件": func(m *update.Manifest) { m.Assets = append(m.Assets, m.Assets[0]) },
		"路径穿越": func(m *update.Manifest) { m.Assets[0].Name = "../file" },
		"凭据地址": func(m *update.Manifest) { m.Assets[0].URL = "https://user:pass@example.com/file" },
		"明文地址": func(m *update.Manifest) { m.Assets[0].URL = "http://example.com/file" },
		"缺少摘要": func(m *update.Manifest) { m.Assets[0].SHA256 = "" },
		"空文件":  func(m *update.Manifest) { m.Assets[0].Size = 0 },
		"错误平台": func(m *update.Manifest) { m.Assets[0].OS = "unknown" },
	} {
		t.Run(name, func(t *testing.T) {
			candidate := manifest
			candidate.Assets = slices.Clone(manifest.Assets)
			change(&candidate)
			require.Error(t, ValidateManifest(candidate))
		})
	}
	_, err := ParseManifest([]byte(strings.Repeat(" ", maxManifestSize+1)))
	require.ErrorContains(t, err, "超过")
}
