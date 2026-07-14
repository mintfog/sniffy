// Copyright 2026 The mintfog Authors
// SPDX-License-Identifier: Apache-2.0
// Use of this source code is governed by an Apache 2.0
// license that can be found in the LICENSE file.

//go:build desktop

package desktop

import (
	"sync"
	"time"

	"github.com/wailsapp/wails/v3/pkg/application"
	"github.com/wailsapp/wails/v3/pkg/events"
)

// childWindowIdleTTL 是工具型子窗口被用户关闭(实为隐藏)后,若一直没再打开,多久真正销毁以回收内存。
//
// 仅 Windows 启用本机制:那里每建一个 WebView2 窗口都要在主线程同步创建 controller(数百毫秒卡顿)。
// 关闭时隐藏而非销毁,可让「活跃使用期内重开」命中热窗口、即时呈现;但隐藏的 WebView2 仍常驻内存,
// 故闲置超过此时长后主动销毁——在「重开顺滑」与「不长期占内存」之间取平衡。macOS/Linux 新建廉价,
// 不接管,保持原生「关闭即销毁」。
const childWindowIdleTTL = 3 * time.Minute

// managedChildWindow 是被接管的单个子窗口的状态。epoch 用于让「已触发但尚未取到锁的销毁回调」失效:
// 复用或重排都会递增 epoch,回调取锁后发现 epoch 不符即放弃销毁。
type managedChildWindow struct {
	win    application.Window
	unhook func() // 注销其 WindowClosing 拦截钩子
	timer  *time.Timer
	epoch  uint64
}

// childWindowManager 管理工具型子窗口的「关闭即隐藏、空闲即销毁」生命周期(仅 Windows 接管)。
//
// 它是被接管窗口的唯一真相源(不依赖 Wails 的 GetByName):销毁一旦提交便把窗口从 wins 中移除,
// 使并发的 reuse 立即返回 nil、调用方改为新建——绝不复用/显示一个正在异步拆除的濒死窗口。
//
// 销毁通过「先注销拦截钩子,再 Close()」实现:Close 会 emit WindowClosing,若拦截钩子仍在会被
// Cancel() 拦回隐藏,故必须先撤钩子,让 Wails 内建监听器真正销毁窗口。
type childWindowManager struct {
	mu   sync.Mutex
	wins map[string]*managedChildWindow // 窗口名 → 接管中的窗口
}

func newChildWindowManager() *childWindowManager {
	return &childWindowManager{wins: map[string]*managedChildWindow{}}
}

// track 接管一个刚创建的子窗口:把用户关闭拦截为隐藏,并在隐藏后安排空闲销毁。
// 覆盖该名下的旧记录(若上一个窗口正在异步销毁,其回调会因 wins[name] 已换人而自动作废)。
//
// 排序说明:先 RegisterHook 再存 mw。理论上若 WindowClosing 在两步之间触发,scheduleDestroy 会因
// wins[name]!=mw 空转,使该隐藏窗口漏排销毁(良性泄漏,非崩溃);但窗口刚创建、用户无从在这几条指令内
// 将其关闭,故不可达。反序(先存 mw 后挂钩子)则会在无钩子的空档里让默认监听器销毁窗口而 mw 仍在表中,
// reuse 取到已销毁窗口——后果更糟,故取此序。
func (m *childWindowManager) track(name string, win application.Window) {
	mw := &managedChildWindow{win: win}
	mw.unhook = win.RegisterHook(events.Common.WindowClosing, func(e *application.WindowEvent) {
		e.Cancel()
		win.Hide()
		m.scheduleDestroy(name, mw)
	})
	m.mu.Lock()
	m.wins[name] = mw
	m.mu.Unlock()
}

// reuse 取该名下可复用的热窗口并取消其待销毁;若无接管记录或销毁已提交(窗口即将拆除)则返回 nil,
// 调用方据此改为新建,避免复用一个濒死窗口。
func (m *childWindowManager) reuse(name string) application.Window {
	m.mu.Lock()
	defer m.mu.Unlock()
	mw := m.wins[name]
	if mw == nil {
		return nil
	}
	if mw.timer != nil {
		mw.timer.Stop()
		mw.timer = nil
	}
	mw.epoch++ // 使任何已触发、正等待本锁的销毁回调失效
	return mw.win
}

// scheduleDestroy 安排在 TTL 后销毁隐藏中的子窗口;每次调用都作废上一次的调度。
func (m *childWindowManager) scheduleDestroy(name string, mw *managedChildWindow) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.wins[name] != mw { // 已被新窗口替换或已移除,忽略
		return
	}
	if mw.timer != nil {
		mw.timer.Stop()
	}
	mw.epoch++
	myEpoch := mw.epoch
	mw.timer = time.AfterFunc(childWindowIdleTTL, func() {
		m.mu.Lock()
		// wins[name] 换人=被新窗口替换;epoch 不符=期间被 reuse 或重新调度过——两者都放弃本次销毁。
		if m.wins[name] != mw || mw.epoch != myEpoch {
			m.mu.Unlock()
			return
		}
		delete(m.wins, name) // 先移除:并发 reuse 随即得 nil→改为新建,不会复用濒死窗口
		unhook := mw.unhook
		m.mu.Unlock()

		if unhook != nil {
			unhook() // 先撤拦截钩子,Close() 才会真正销毁而非又被隐藏
		}
		mw.win.Close()
	})
}
