// Copyright 2026 The mintfog Authors
// SPDX-License-Identifier: Apache-2.0
// Use of this source code is governed by an Apache 2.0
// license that can be found in the LICENSE file.

//go:build desktop

package desktop

import (
	"context"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/wailsapp/wails/v3/pkg/application"
	"github.com/wailsapp/wails/v3/pkg/updater"

	"github.com/mintfog/sniffy/internal/app"
	"github.com/mintfog/sniffy/internal/service"
	"github.com/mintfog/sniffy/internal/update"
	"github.com/mintfog/sniffy/internal/version"
)

// appReplacer 把已下载并校验过的应用归档交给 Wails 替换器:它在应用旁解开归档,
// 应用退出后由以辅助模式重新启动的本程序换入新应用包并重新打开,失败时恢复原应用。
type appReplacer struct {
	mu     sync.Mutex
	inited bool
	// handedOff 在辅助进程接手、应用开始退出后置位;此后 Wails 替换器对任何调用
	// 都返回 ErrRestartPending,重复的安装请求直接视为成功。
	handedOff bool
	source    localArchive
}

// localArchive 是只提供一个本地文件的更新源,字段在 appReplacer.mu 内写入和读取。
type localArchive struct {
	file   service.UpdateFile
	digest []byte
}

func (l *localArchive) Name() string { return "sniffy-local" }

func (l *localArchive) Check(context.Context, updater.CheckRequest) (*updater.Release, error) {
	return &updater.Release{
		Version: l.file.Version,
		Artifact: updater.Artifact{
			Filename: filepath.Base(l.file.Path),
			Size:     l.file.Asset.Size,
		},
		// 替换器在复制时重新计算摘要,下载后到安装前被改动的文件会被拒绝。
		Verification: &updater.Verification{DigestAlgo: "sha256", Digest: l.digest},
	}, nil
}

func (l *localArchive) Download(_ context.Context, _ *updater.Release, dst io.Writer, onProgress func(written, total int64)) error {
	f, err := os.Open(l.file.Path)
	if err != nil {
		return err
	}
	defer f.Close()
	n, err := io.Copy(dst, f)
	onProgress(n, n)
	return err
}

// replace 暂存已下载的应用归档并退出当前进程;成功返回后应用随即退出。
// 交接前失败时放弃本次运行的原地替换。下载记录在锁内读取:并发的安装请求
// 可能已放弃替换,此时不能再拿旧记录交接。
func (r *appReplacer) replace(svc *service.Service, logger *app.Logger) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.handedOff {
		return nil
	}
	file, err := svc.DownloadedUpdate()
	if err != nil {
		return err
	}
	if file.Action != update.ActionReplace {
		return errors.New("已下载的安装包不再用于替换应用,请重新安装")
	}
	if err := r.handOff(svc, file, logger); err != nil {
		svc.AbandonAppReplace()
		return err
	}
	r.handedOff = true
	logger.Info("已暂存 %s,退出后替换应用", file.Version)
	return nil
}

func (r *appReplacer) handOff(svc *service.Service, file service.UpdateFile, logger *app.Logger) error {
	digest, err := hex.DecodeString(file.Asset.SHA256)
	if err != nil {
		return fmt.Errorf("应用归档的摘要无效: %w", err)
	}
	wapp := application.Get()
	if wapp == nil {
		return errors.New("窗口系统不可用")
	}
	if !r.inited {
		err := wapp.Updater.Init(updater.Config{
			CurrentVersion: version.Get(),
			Providers:      []updater.Provider{&r.source},
			Window:         updater.WindowNone,
		})
		if err != nil {
			return fmt.Errorf("初始化应用替换失败: %w", err)
		}
		r.inited = true
	}
	r.source = localArchive{file: file, digest: digest}

	ctx := context.Background()
	if _, err := wapp.Updater.Check(ctx); err != nil {
		return fmt.Errorf("准备新版本失败: %w", err)
	}
	if err := wapp.Updater.DownloadAndInstall(ctx); err != nil {
		return fmt.Errorf("准备新版本失败: %w", err)
	}
	if err := svc.BeginAppReplace(file.Version); err != nil {
		logger.Warn("记录替换目标版本失败,下次启动无法察觉替换是否生效: %v", err)
	}
	if err := wapp.Updater.Restart(ctx); err != nil {
		// 辅助进程未接手时暂存目录交回本进程;此后不再使用应用归档,随即清掉。
		if staging := filepath.Dir(wapp.Updater.DownloadedPath()); strings.HasPrefix(filepath.Base(staging), stagingPrefix) {
			_ = os.RemoveAll(staging)
		}
		return fmt.Errorf("启动应用替换失败: %w", err)
	}
	return nil
}

// stagingPrefix 是 Wails 替换器在应用包同级创建的暂存目录名前缀。
const stagingPrefix = "wails-update-"

// staleStagingAge 是启动清理认定暂存目录已废弃的最短闲置时长。旧进程退出、单实例锁释放后,
// 辅助进程仍要等旧进程完全退出、备份原应用、换入并校验新应用包,期间重新打开的应用
// 不能删掉它正要换入的暂存目录。暂存目录的修改时间在解包出应用包时刷新,
// 正常流程随后数分钟内必然结束,超过该时长仍在的只会是残留。
const staleStagingAge = time.Hour

// archiveAppName 是应用归档唯一的顶层条目,即暂存目录内应用包的名字;
// 与本地应用包是否被用户改名无关。
const archiveAppName = "Sniffy.app"

// removeStaleStaging 删除当前应用包同级、内含应用包且闲置超过 staleStagingAge 的 Wails 暂存目录。
// 替换器只在辅助进程换入成功后删除暂存目录;辅助进程放弃替换或进程中途退出时,
// 完整的应用包会留在应用目录里,被 Spotlight 与启动台收录。不是从应用包运行时什么也不做。
func removeStaleStaging() {
	if bundle := update.RunningAppBundle(); bundle != "" {
		removeStagingBeside(filepath.Dir(bundle))
	}
}

// removeStagingBeside 只删内含本应用归档解出的应用包的暂存目录:同一目录下其他 Wails 应用的
// 暂存目录可能正被它们的更新流程使用。
func removeStagingBeside(parent string) {
	entries, err := os.ReadDir(parent)
	if err != nil {
		return
	}
	for _, e := range entries {
		if !e.IsDir() || !strings.HasPrefix(e.Name(), stagingPrefix) {
			continue
		}
		if info, err := e.Info(); err != nil || time.Since(info.ModTime()) < staleStagingAge {
			continue
		}
		dir := filepath.Join(parent, e.Name())
		if info, err := os.Lstat(filepath.Join(dir, archiveAppName)); err == nil && info.IsDir() {
			_ = os.RemoveAll(dir)
		}
	}
}
