// Copyright 2026 The mintfog Authors
// SPDX-License-Identifier: Apache-2.0
// Use of this source code is governed by an Apache 2.0
// license that can be found in the LICENSE file.

package service

import (
	"context"
	"errors"
	"os"
	"sync"
	"time"

	"github.com/mintfog/sniffy/internal/core"
	"github.com/mintfog/sniffy/internal/platform"
	"github.com/mintfog/sniffy/internal/update"
	"github.com/mintfog/sniffy/internal/version"
)

// 限制多个清单源依次超时的累计耗时。
const updateCheckTimeout = 30 * time.Second

// 更新状态与前端 UpdateState.status 的取值保持一致。
const (
	UpdateStatusIdle        = "idle"
	UpdateStatusChecking    = "checking"
	UpdateStatusLatest      = "latest"
	UpdateStatusAvailable   = "available"
	UpdateStatusDownloading = "downloading"
	UpdateStatusDownloaded  = "downloaded"
	UpdateStatusError       = "error"
)

// UpdateAssetDTO 是匹配当前平台的下载产物。
type UpdateAssetDTO struct {
	Name string `json:"name"`
	URL  string `json:"url"`
	Size int64  `json:"size"`
}

// UpdateStateDTO 通过调用返回值与 update_state 事件传递完整快照。
type UpdateStateDTO struct {
	// Revision 供前端丢弃经不同通道迟到的旧快照。
	Revision uint64 `json:"revision"`
	Status   string `json:"status"`
	Current  string `json:"current"`
	Latest   string `json:"latest,omitempty"`
	// PublishedAt 保留清单中的日期或 RFC3339 时间字符串。
	PublishedAt string          `json:"publishedAt,omitempty"`
	NotesURL    string          `json:"notesUrl,omitempty"`
	Asset       *UpdateAssetDTO `json:"asset,omitempty"`
	CheckedAt   string          `json:"checkedAt,omitempty"`
	Error       string          `json:"error,omitempty"`
	// ErrorStage 区分检查与下载失败,取值 check / download;没有错误时为空。
	ErrorStage string `json:"errorStage,omitempty"`
	// Notify 在线上版本较新且未被跳过时为真。
	Notify         bool   `json:"notify"`
	SkippedVersion string `json:"skippedVersion,omitempty"`
	AutoCheck      bool   `json:"autoCheck"`
	DevBuild       bool   `json:"devBuild,omitempty"`
	DownloadedPath string `json:"downloadedPath,omitempty"`
	Downloaded     int64  `json:"downloaded,omitempty"`
	Total          int64  `json:"total,omitempty"`
	// InstallAction 由产物类型与构建平台决定,取值 run / open / reveal。
	InstallAction string `json:"installAction"`
}

// updateStore 保存运行期状态;自动检查开关与跳过版本由 configStore 持久化。
type updateStore struct {
	mu      sync.Mutex
	checker *update.Checker
	state   updateSnapshot
	// rev 在修改 state、checker 或更新配置时递增。
	rev uint64
	// cancel 与 done 属于当前这次下载,没有进行中的下载时都为 nil。
	// done 在下载收口(状态已写定、事件已发出)后关闭。
	cancel context.CancelFunc
	done   chan struct{}
	// destDir 为空时按 platform.DownloadsDir 解析,测试据此改写落盘位置。
	destDir string
}

type updateSnapshot struct {
	status         string
	latest         string
	publishedAt    string
	notesURL       string
	asset          update.Asset
	hasAsset       bool
	available      bool
	dev            bool
	checkedAt      time.Time
	err            string
	errorStage     string
	downloadedPath string
	done           int64
	total          int64
}

func newUpdateStore() *updateStore {
	return &updateStore{
		checker: update.NewChecker(version.Get()),
		state:   updateSnapshot{status: UpdateStatusIdle},
	}
}

// SetUpdateChecker 替换清单查询器,供测试指向本地服务。
func (s *Service) SetUpdateChecker(c *update.Checker) {
	if c == nil {
		return
	}
	s.updates.mu.Lock()
	s.updates.checker = c
	s.updates.rev++
	s.updates.mu.Unlock()
}

// SetUpdateDownloadDir 固定安装包的落盘目录;空串表示按平台默认解析。
func (s *Service) SetUpdateDownloadDir(dir string) {
	s.updates.mu.Lock()
	s.updates.destDir = dir
	s.updates.mu.Unlock()
}

// UpdateState 返回当前的更新状态快照。
//
// 配置必须在锁内读取,避免旧配置与已递增的 Revision 组成快照。
func (s *Service) UpdateState() UpdateStateDTO {
	s.updates.mu.Lock()
	defer s.updates.mu.Unlock()
	return s.updates.snapshotLocked(s.cfg.get())
}

func (u *updateStore) snapshotLocked(cfg AppConfig) UpdateStateDTO {
	st := u.state
	dto := UpdateStateDTO{
		Revision:       u.rev,
		Status:         st.status,
		Current:        u.checker.Current,
		Latest:         st.latest,
		PublishedAt:    st.publishedAt,
		NotesURL:       st.notesURL,
		Error:          st.err,
		ErrorStage:     st.errorStage,
		SkippedVersion: cfg.UpdateSkipped,
		AutoCheck:      cfg.UpdateCheck,
		DevBuild:       st.dev,
		DownloadedPath: st.downloadedPath,
		Downloaded:     st.done,
		Total:          st.total,
		InstallAction:  st.asset.InstallAction(),
	}
	if !st.checkedAt.IsZero() {
		dto.CheckedAt = st.checkedAt.UTC().Format(time.RFC3339)
	}
	if st.hasAsset {
		dto.Asset = &UpdateAssetDTO{Name: st.asset.Name, URL: st.asset.URL, Size: st.asset.Size}
	}
	dto.Notify = st.available && st.latest != cfg.UpdateSkipped
	return dto
}

func (s *Service) emitUpdateState() {
	s.emit(core.EventUpdateState, s.UpdateState())
}

// ShouldAutoCheckUpdate 表示是否该执行启动后的静默检查:用户没关掉,且不是开发构建。
func (s *Service) ShouldAutoCheckUpdate() bool {
	if !s.cfg.get().UpdateCheck {
		return false
	}
	s.updates.mu.Lock()
	defer s.updates.mu.Unlock()
	return !update.IsDevBuild(s.updates.checker.Current)
}

// CheckForUpdate 查询发布清单并更新状态。已有检查或下载在进行时直接返回当前快照。
func (s *Service) CheckForUpdate(ctx context.Context) (UpdateStateDTO, error) {
	u := s.updates
	u.mu.Lock()
	if u.state.status == UpdateStatusChecking || u.state.status == UpdateStatusDownloading {
		state := u.snapshotLocked(s.cfg.get())
		u.mu.Unlock()
		return state, nil
	}
	u.state.status = UpdateStatusChecking
	u.state.err, u.state.errorStage = "", ""
	u.rev++
	checker := u.checker
	u.mu.Unlock()
	s.emitUpdateState()

	ctx, cancel := context.WithTimeout(ctx, updateCheckTimeout)
	defer cancel()
	res, err := checker.Check(ctx)

	u.mu.Lock()
	u.state.checkedAt = time.Now()
	if err != nil {
		u.state.err = err.Error()
		u.state.errorStage = "check"
		// 检查失败时保留本地安装入口,失败原因随 Error 展示。
		if installerExists(u.state.downloadedPath) {
			u.state.status = UpdateStatusDownloaded
		} else {
			u.dropDownloadLocked()
			u.state.status = UpdateStatusError
		}
	} else {
		u.applyResultLocked(res)
	}
	u.rev++
	u.mu.Unlock()

	s.emitUpdateState()
	return s.UpdateState(), err
}

func (u *updateStore) applyResultLocked(res update.Result) {
	// 只有仍在原处且对应本次版本的安装包才能继续使用。
	if u.state.latest != res.Manifest.Version || !installerExists(u.state.downloadedPath) {
		u.dropDownloadLocked()
	}
	u.state.err, u.state.errorStage = "", ""
	u.state.latest = res.Manifest.Version
	u.state.publishedAt = res.Manifest.PublishedAt
	u.state.notesURL = res.Manifest.NotesURL
	u.state.asset, u.state.hasAsset = res.Asset, res.HasAsset
	u.state.available = res.Available
	u.state.dev = res.Dev
	switch {
	case u.state.downloadedPath != "":
		u.state.status = UpdateStatusDownloaded
	case res.Available:
		u.state.status = UpdateStatusAvailable
	default:
		u.state.status = UpdateStatusLatest
	}
}

// StartUpdateDownload 启动后台下载并返回当前快照;进度与结果随 update_state 事件推送。
//
// 下载独立于 REST 请求生命周期,由 CancelUpdateDownload 取消。
func (s *Service) StartUpdateDownload() (UpdateStateDTO, error) {
	u := s.updates
	u.mu.Lock()
	if u.state.status == UpdateStatusDownloading {
		state := u.snapshotLocked(s.cfg.get())
		u.mu.Unlock()
		return state, nil
	}
	if u.state.status == UpdateStatusChecking {
		state := u.snapshotLocked(s.cfg.get())
		u.mu.Unlock()
		return state, errors.New("正在检查更新,请稍后再下载")
	}
	if !u.state.hasAsset {
		state := u.snapshotLocked(s.cfg.get())
		u.mu.Unlock()
		return state, errors.New("当前平台没有可下载的安装包,请到官网下载页自取")
	}
	asset, checker := u.state.asset, u.checker
	destDir := u.destDir
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	u.cancel, u.done = cancel, done
	u.state.status = UpdateStatusDownloading
	u.state.err, u.state.errorStage = "", ""
	u.state.downloadedPath = ""
	u.state.done, u.state.total = 0, asset.Size
	u.rev++
	u.mu.Unlock()
	s.emitUpdateState()

	go func() {
		defer close(done)
		defer cancel()
		var err error
		if destDir == "" {
			if destDir, err = platform.DownloadsDir(); err != nil {
				s.finishUpdateDownload("", err)
				return
			}
		}
		path, err := checker.Download(ctx, asset, destDir, func(p update.Progress) {
			u.mu.Lock()
			u.state.done, u.state.total = p.Done, p.Total
			u.rev++
			u.mu.Unlock()
			s.emitUpdateState()
		})
		s.finishUpdateDownload(path, err)
	}()
	return s.UpdateState(), nil
}

func (s *Service) finishUpdateDownload(path string, err error) {
	u := s.updates
	u.mu.Lock()
	u.cancel, u.done = nil, nil
	switch {
	case err == nil:
		u.state.status = UpdateStatusDownloaded
		u.state.downloadedPath = path
		u.state.err, u.state.errorStage = "", ""
	case errors.Is(err, context.Canceled):
		u.state.status = statusWithoutDownload(u.state)
		u.state.done, u.state.total = 0, 0
		u.state.err, u.state.errorStage = "", ""
	default:
		u.state.status = UpdateStatusError
		u.state.err = err.Error()
		u.state.errorStage = "download"
	}
	u.rev++
	u.mu.Unlock()
	s.emitUpdateState()
}

func statusWithoutDownload(st updateSnapshot) string {
	if st.available {
		return UpdateStatusAvailable
	}
	return UpdateStatusLatest
}

func (u *updateStore) dropDownloadLocked() {
	u.state.downloadedPath = ""
	u.state.done, u.state.total = 0, 0
	if u.state.status == UpdateStatusDownloaded {
		u.state.status = statusWithoutDownload(u.state)
	}
}

func installerExists(path string) bool {
	if path == "" {
		return false
	}
	info, err := os.Stat(path)
	return err == nil && info.Mode().IsRegular()
}

// DownloadedUpdate 返回仍在原处的更新文件路径与安装动作。
// 文件缺失时清除下载记录并广播状态,让界面恢复下载入口。
func (s *Service) DownloadedUpdate() (path, action string, err error) {
	u := s.updates
	u.mu.Lock()
	path = u.state.downloadedPath
	if path == "" {
		u.mu.Unlock()
		return "", "", errors.New("还没有已下载的安装包")
	}
	if installerExists(path) {
		action = u.state.asset.InstallAction()
		u.mu.Unlock()
		return path, action, nil
	}
	u.dropDownloadLocked()
	u.rev++
	u.mu.Unlock()
	s.emitUpdateState()
	return "", "", errors.New("安装包已不在原处,请重新下载")
}

// CancelUpdateDownload 取消下载,最多等待 cancelWaitTimeout 后返回当前状态。
// 等待下载清理临时文件并更新状态;超时时快照可能仍处于下载中。
func (s *Service) CancelUpdateDownload() UpdateStateDTO {
	s.updates.mu.Lock()
	cancel, done := s.updates.cancel, s.updates.done
	s.updates.mu.Unlock()
	if cancel != nil {
		cancel()
		// 网络请求可被取消,磁盘写入可能仍阻塞,因此限制等待时间。
		select {
		case <-done:
		case <-time.After(cancelWaitTimeout):
		}
	}
	return s.UpdateState()
}

const cancelWaitTimeout = 5 * time.Second

// SkipUpdateVersion 记下不再提醒的版本号(空串表示跳过当前查到的最新版),更新的版本照常提醒。
// 传入空串且尚未查到版本时是空操作。
func (s *Service) SkipUpdateVersion(v string) UpdateStateDTO {
	if v == "" {
		s.updates.mu.Lock()
		v = s.updates.state.latest
		s.updates.mu.Unlock()
	}
	if v == "" {
		return s.UpdateState()
	}
	s.UpdateConfig(map[string]any{"updateSkipped": v})
	return s.UpdateState()
}

// SetUpdateAutoCheck 持久化自动检查开关,并广播状态以同步各窗口。
func (s *Service) SetUpdateAutoCheck(enabled bool) UpdateStateDTO {
	s.UpdateConfig(map[string]any{"updateCheck": enabled})
	return s.UpdateState()
}

// ClearSkippedUpdateVersion 撤销「跳过此版本」,让该版本重新参与提醒。
func (s *Service) ClearSkippedUpdateVersion() UpdateStateDTO {
	s.UpdateConfig(map[string]any{"updateSkipped": ""})
	return s.UpdateState()
}
