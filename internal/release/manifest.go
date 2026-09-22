// Copyright 2026 The mintfog Authors
// SPDX-License-Identifier: Apache-2.0

// Package release 生成发布清单并将已校验的制品发布到 R2。
package release

import (
	"bytes"
	"cmp"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"time"

	"github.com/mintfog/sniffy/internal/update"
	"golang.org/x/mod/semver"
)

const maxManifestSize = 1 << 20

// 官网用 JavaScript Number 读取文件大小，清单中的整数必须能被精确表示。
const maxAssetSize = 1<<53 - 1

var (
	// semver 接受 v1、v1.2 等简写，发布标签要求完整的三段版本号。
	versionCore  = regexp.MustCompile(`^v[0-9]+\.[0-9]+\.[0-9]+(?:[-+]|$)`)
	assetName    = regexp.MustCompile(`^sniffy-[a-z0-9.-]+$`)
	artifactName = regexp.MustCompile(`^sniffy-(desktop-)?(darwin|linux|windows)-(amd64|arm64)(-installer\.(dmg|deb|exe)|\.exe)?$`)
)

// ParseVersion 接受带可选 v 前缀的完整 SemVer，返回不带前缀的版本号。
func ParseVersion(value string) (string, error) {
	version := strings.TrimPrefix(value, "v")
	tag := "v" + version
	if !versionCore.MatchString(tag) || !semver.IsValid(tag) {
		return "", fmt.Errorf("版本号无效：%s", value)
	}
	return version, nil
}

type ManifestOptions struct {
	Directory       string
	Version         string
	BaseURL         string
	NotesURL        string
	PublishedAt     string
	RequireComplete bool
}

// RequiredArtifacts 返回完整发布所需的制品名，与 build.yml 的构建目标保持一致。
func RequiredArtifacts() []string {
	var names []string
	for _, goos := range []string{"darwin", "linux", "windows"} {
		for _, arch := range []string{"amd64", "arm64"} {
			name := "sniffy-" + goos + "-" + arch
			if goos == "windows" {
				name += ".exe"
			}
			names = append(names, name)
		}
	}
	targets := []struct{ os, arch, extension string }{
		{"darwin", "amd64", "dmg"},
		{"darwin", "arm64", "dmg"},
		{"linux", "amd64", "deb"},
		{"windows", "amd64", "exe"},
	}
	for _, target := range targets {
		name := "sniffy-desktop-" + target.os + "-" + target.arch
		binary := name
		if target.os == "windows" {
			binary += ".exe"
		}
		names = append(names, binary, name+"-installer."+target.extension)
	}
	return names
}

// GenerateManifest 按实际文件计算摘要；完整发布要求所有目标制品都存在。
func GenerateManifest(options ManifestOptions) (update.Manifest, error) {
	version, err := ParseVersion(options.Version)
	if err != nil {
		return update.Manifest{}, err
	}
	if options.BaseURL == "" {
		options.BaseURL = "https://cdn.gosniffy.com/releases"
	}
	base, err := httpsURL(options.BaseURL)
	if err != nil || base.RawQuery != "" || base.ForceQuery || base.Fragment != "" {
		return update.Manifest{}, fmt.Errorf("下载根地址必须是 HTTPS 地址，且不带凭据、查询参数或片段")
	}
	if options.PublishedAt == "" {
		options.PublishedAt = time.Now().UTC().Format(time.DateOnly)
	}
	manifest := update.Manifest{
		Version:     version,
		PublishedAt: options.PublishedAt,
		NotesURL:    options.NotesURL,
	}
	entries, err := os.ReadDir(options.Directory)
	if err != nil {
		return manifest, err
	}
	versionURL := strings.TrimRight(base.String(), "/") + "/v" + version
	names := make(map[string]bool)
	for _, entry := range entries {
		asset, ok := classify(entry.Name())
		if !ok {
			if strings.HasPrefix(entry.Name(), "sniffy-") {
				return manifest, fmt.Errorf("无法识别发布制品：%s", entry.Name())
			}
			continue
		}
		data, err := readArtifact(filepath.Join(options.Directory, asset.Name))
		if err != nil {
			return manifest, err
		}
		asset.Size = int64(len(data))
		asset.SHA256 = checksum(data)
		asset.URL = versionURL + "/" + asset.Name
		manifest.Assets = append(manifest.Assets, asset)
		names[asset.Name] = true
	}
	if options.RequireComplete {
		var missing []string
		for _, name := range RequiredArtifacts() {
			if !names[name] {
				missing = append(missing, name)
			}
		}
		if len(missing) > 0 {
			return manifest, fmt.Errorf("缺少发布制品：%s", strings.Join(missing, "、"))
		}
	}
	// 客户端取同平台的首个产物，安装包必须排在免安装文件之前。
	slices.SortFunc(manifest.Assets, func(a, b update.Asset) int {
		return cmp.Or(
			cmp.Compare(a.OS, b.OS),
			cmp.Compare(a.Arch, b.Arch),
			cmp.Compare(b.Kind, a.Kind),
			cmp.Compare(a.Name, b.Name),
		)
	})
	return manifest, ValidateManifest(manifest)
}

func classify(name string) (update.Asset, bool) {
	parts := artifactName.FindStringSubmatch(name)
	if parts == nil {
		return update.Asset{}, false
	}
	asset := update.Asset{
		OS:      parts[2],
		Arch:    parts[3],
		Edition: "headless",
		Kind:    update.KindBinary,
		Name:    name,
	}
	if parts[1] != "" {
		asset.Edition = "desktop"
	}
	suffix, installerExtension := parts[4], parts[5]
	if installerExtension != "" {
		asset.Kind = update.KindInstaller
		extension := map[string]string{"darwin": "dmg", "linux": "deb", "windows": "exe"}[asset.OS]
		return asset, asset.Edition == "desktop" && installerExtension == extension
	}
	extension := ""
	if asset.OS == "windows" {
		extension = ".exe"
	}
	return asset, suffix == extension
}

// ValidateManifest 校验发布端需要的完整版本、唯一文件名、平台和摘要约束。
func ValidateManifest(manifest update.Manifest) error {
	version, err := ParseVersion(manifest.Version)
	if err != nil {
		return err
	}
	if version != manifest.Version || len(manifest.Assets) == 0 {
		return fmt.Errorf("发布清单格式无效")
	}
	names := make(map[string]bool)
	for _, asset := range manifest.Assets {
		if !assetName.MatchString(asset.Name) || names[asset.Name] {
			return fmt.Errorf("发布清单的文件名无效或重复：%s", asset.Name)
		}
		names[asset.Name] = true
		validOS := slices.Contains([]string{"darwin", "linux", "windows"}, asset.OS)
		validArch := slices.Contains([]string{"amd64", "arm64", "universal"}, asset.Arch)
		validKind := asset.Kind == update.KindInstaller || asset.Kind == update.KindBinary
		validEdition := asset.Edition == "desktop" || asset.Edition == "headless"
		if !validOS || !validArch || !validKind || !validEdition {
			return fmt.Errorf("发布清单的平台或类型无效：%s", asset.Name)
		}
		if _, err := httpsURL(asset.URL); err != nil {
			return fmt.Errorf("制品 %s：%w", asset.Name, err)
		}
		sum, err := hex.DecodeString(asset.SHA256)
		validChecksum := err == nil && len(sum) == sha256.Size && strings.ToLower(asset.SHA256) == asset.SHA256
		validSize := asset.Size > 0 && asset.Size <= maxAssetSize
		if !validChecksum || !validSize {
			return fmt.Errorf("发布清单的大小或摘要无效：%s", asset.Name)
		}
	}
	return nil
}

func httpsURL(value string) (*url.URL, error) {
	u, err := url.Parse(value)
	if err != nil || u.Scheme != "https" || u.Hostname() == "" || u.User != nil {
		return nil, fmt.Errorf("必须使用不带凭据的 HTTPS 地址：%s", value)
	}
	return u, nil
}

// ParseManifest 限制清单大小，并按发布端约束校验内容。
func ParseManifest(data []byte) (update.Manifest, error) {
	var manifest update.Manifest
	if len(data) > maxManifestSize {
		return manifest, fmt.Errorf("发布清单超过 %d 字节", maxManifestSize)
	}
	if err := json.Unmarshal(data, &manifest); err != nil {
		return manifest, err
	}
	return manifest, ValidateManifest(manifest)
}

// EncodeManifest 生成带缩进和末尾换行的 JSON，保留 URL 中的 HTML 字符。
func EncodeManifest(manifest update.Manifest) ([]byte, error) {
	var buffer bytes.Buffer
	encoder := json.NewEncoder(&buffer)
	encoder.SetEscapeHTML(false)
	encoder.SetIndent("", "  ")
	err := encoder.Encode(manifest)
	return buffer.Bytes(), err
}

// Checksums 生成与清单一致的 SHA256SUMS 内容。
func Checksums(manifest update.Manifest) []byte {
	var out strings.Builder
	for _, asset := range manifest.Assets {
		fmt.Fprintf(&out, "%s  %s\n", asset.SHA256, asset.Name)
	}
	return []byte(out.String())
}

func readArtifact(path string) ([]byte, error) {
	info, err := os.Lstat(path)
	if err != nil {
		return nil, err
	}
	if !info.Mode().IsRegular() || info.Size() <= 0 {
		return nil, fmt.Errorf("制品必须是非空普通文件：%s", path)
	}
	return os.ReadFile(path)
}

func checksum(data []byte) string {
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:])
}

// WriteFile 原子替换本地清单或校验和，失败时保留已有文件。
func WriteFile(path string, data []byte) error {
	if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
		return err
	}
	file, err := os.CreateTemp(filepath.Dir(path), ".release-*")
	if err != nil {
		return err
	}
	defer os.Remove(file.Name())
	_, writeErr := file.Write(data)
	closeErr := file.Close()
	if writeErr != nil {
		return writeErr
	}
	if closeErr != nil {
		return closeErr
	}
	return os.Rename(file.Name(), path)
}
