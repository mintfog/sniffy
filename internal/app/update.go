// Copyright 2026 The mintfog Authors
// SPDX-License-Identifier: Apache-2.0
// Use of this source code is governed by an Apache 2.0
// license that can be found in the LICENSE file.

package app

import "time"

const (
	// 首次联网检查错开引擎与界面启动。
	updateFirstDelay = 8 * time.Second
	updateInterval   = time.Hour
)

// StartUpdateCheck 启动后台版本检查,由 Stop 取消。自动检查开关在每轮开始时读取。
func (a *App) StartUpdateCheck() {
	if a.updateCtx == nil {
		return
	}
	go func() {
		timer := time.NewTimer(updateFirstDelay)
		defer timer.Stop()
		for {
			select {
			case <-a.updateCtx.Done():
				return
			case <-timer.C:
			}
			a.checkUpdateOnce()
			timer.Reset(updateInterval)
		}
	}()
}

func (a *App) checkUpdateOnce() {
	if !a.Service.ShouldAutoCheckUpdate() {
		return
	}
	state, err := a.Service.CheckForUpdate(a.updateCtx)
	if err != nil {
		// 后台检查失败不影响代理工作,仅记调试日志。
		a.Logger.Debug("检查新版本失败: %v", err)
		return
	}
	if state.Notify {
		a.Logger.Info("有新版本 %s 可用(当前 %s)", state.Latest, state.Current)
	}
}

func (a *App) stopUpdateCheck() {
	if a.updateCancel != nil {
		a.updateCancel()
	}
}
