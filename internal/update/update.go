// Copyright 2026 The mintfog Authors
// SPDX-License-Identifier: Apache-2.0
// Use of this source code is governed by an Apache 2.0
// license that can be found in the LICENSE file.

// Package update 从发布清单查询新版本并下载对应平台的安装包。
package update

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"net/url"
	"strings"
)

// Manifest 是客户端与官网共用的发布清单，包含版本信息和各平台产物的下载及校验信息。
type Manifest struct {
	Version string `json:"version"`
	// PublishedAt 为日期或 RFC3339 时间,仅供展示,不参与新旧判定。
	PublishedAt string `json:"publishedAt"`
	// NotesURL 指向该版本的更新说明页;为空时前端不渲染入口。
	NotesURL string  `json:"notesUrl"`
	Assets   []Asset `json:"assets"`
}

// Asset 是清单里的一个下载产物。
type Asset struct {
	OS   string `json:"os"`   // GOOS
	Arch string `json:"arch"` // GOARCH;"universal" 匹配该系统的任意架构
	// Edition 区分桌面版与无界面版,取值 desktop / headless。
	Edition string `json:"edition"`
	// Kind 区分安装包与免安装可执行文件,取值见 KindInstaller / KindBinary。
	Kind string `json:"kind"`
	Name string `json:"name"`
	URL  string `json:"url"`
	Size int64  `json:"size"`
	// SHA256 为十六进制摘要。
	SHA256 string `json:"sha256"`
}

const (
	// KindInstaller 是交给系统或安装程序处理的安装介质(exe / dmg / deb)。
	KindInstaller = "installer"
	// KindBinary 是下载后直接运行、或由用户替换原程序的可执行文件。
	KindBinary = "binary"
)

const archUniversal = "universal"

const (
	// ActionRun 能直接运行安装程序,应用退出后由它接管。
	ActionRun = "run"
	// ActionOpen 只能把安装介质交给系统打开,后续步骤由用户完成。
	ActionOpen = "open"
	// ActionReveal 只能打开文件所在目录。
	ActionReveal = "reveal"
)

// InstallAction 按产物类型与构建平台决定安装动作,免安装程序交由用户手动替换。
func (a Asset) InstallAction() string {
	if a.Kind == KindInstaller {
		return installerAction
	}
	return ActionReveal
}

// ErrNoManifest 表示所有清单源都没能取到可用清单。
var ErrNoManifest = errors.New("update: 没有可用的发布清单源")

// Validate 校验版本号、产物类型、HTTPS 地址、大小与 SHA256。
// 大小与摘要必须来自受信任的清单,用于验证单独托管的安装包。
func (m Manifest) Validate() error {
	if strings.TrimSpace(m.Version) == "" {
		return errors.New("update: 清单缺少版本号")
	}
	if _, ok := parseSemver(m.Version); !ok {
		return fmt.Errorf("update: 清单版本号 %q 不是合法语义化版本", m.Version)
	}
	for _, a := range m.Assets {
		if err := a.validate(); err != nil {
			return err
		}
	}
	return nil
}

func (a Asset) validate() error {
	if strings.TrimSpace(a.Name) == "" {
		return errors.New("update: 产物缺少文件名")
	}
	if a.Kind != KindInstaller && a.Kind != KindBinary {
		return fmt.Errorf("update: 产物 %s 的类型 %q 不合法", a.Name, a.Kind)
	}
	u, err := url.Parse(a.URL)
	if err != nil {
		return fmt.Errorf("update: 产物 %s 的地址无法解析: %w", a.Name, err)
	}
	if u.Scheme != "https" || u.Host == "" {
		return fmt.Errorf("update: 产物 %s 的下载地址不是 https 绝对地址", a.Name)
	}
	if a.Size <= 0 {
		return fmt.Errorf("update: 产物 %s 缺少体积", a.Name)
	}
	if sum, err := hex.DecodeString(a.SHA256); err != nil || len(sum) != sha256.Size {
		return fmt.Errorf("update: 产物 %s 的 SHA256 %q 不合法", a.Name, a.SHA256)
	}
	return nil
}

// AssetFor 挑出匹配指定系统/架构/形态的产物。
// 同一平台登记了多个产物时取清单里的第一个,发布方据此决定优先推荐哪种包。
func (m Manifest) AssetFor(goos, goarch, edition string) (Asset, bool) {
	for _, a := range m.Assets {
		if a.OS != goos || a.Edition != edition {
			continue
		}
		if a.Arch == goarch || a.Arch == archUniversal {
			return a, true
		}
	}
	return Asset{}, false
}
