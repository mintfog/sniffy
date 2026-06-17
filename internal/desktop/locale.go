// Copyright 2026 The mintfog Authors
// SPDX-License-Identifier: Apache-2.0
// Use of this source code is governed by an Apache 2.0
// license that can be found in the LICENSE file.

//go:build desktop

package desktop

import "strings"

// 本文件只负责 React 挂载前的「原生占位 UI」语言：启动期的 macOS 占位菜单与窗口描述。
//
// 设计上 Go 端不持有界面语言这个状态，也不与前端来回同步——它只是按机器语言（OS 偏好语言）
// 把占位 UI 渲染成最可能正确的那一种，避免英文默认菜单闪现。前端就绪后会经 Bridge.SetMenu
// 下发用户实际选择语言的整棵菜单，覆盖此占位。故这里的少量静态标签不构成「Go 端多语言系统」。

// uiLang 返回占位 UI 使用的语言："en" | "zh-Hans" | "zh-Hant"，取自机器语言。
func uiLang() string { return normalizeLocale(osPreferredLang()) }

// normalizeLocale 把任意 BCP-47 / POSIX locale 串归一到三种受支持语言之一。
//   - zh*-Hant / zh-TW / zh-HK / zh-MO → 繁体中文
//   - 其余 zh*（zh / zh-Hans / zh-CN / zh-SG）→ 简体中文
//   - 其它一律英文
func normalizeLocale(s string) string {
	l := strings.ToLower(s)
	if strings.HasPrefix(l, "zh") {
		if strings.Contains(l, "hant") || strings.Contains(l, "tw") ||
			strings.Contains(l, "hk") || strings.Contains(l, "mo") {
			return "zh-Hant"
		}
		return "zh-Hans"
	}
	return "en"
}

// menuLabels 是占位菜单 + 窗口描述所需的全部静态标签。
type menuLabels struct {
	about, services, hide, hideOthers, showAll, quit string
	edit, undo, redo, cut, copy, paste               string
	window, minimise, zoom, closeWindow              string
	show                                             string // 系统托盘菜单：显示/聚焦主窗口
	description                                      string
}

// labelsFor 返回某语言下的占位标签。沿用各平台/Apple 本地化习惯用词。
func labelsFor(lang string) menuLabels {
	switch lang {
	case "en":
		return menuLabels{
			about: "About Sniffy", services: "Services", hide: "Hide Sniffy",
			hideOthers: "Hide Others", showAll: "Show All", quit: "Quit Sniffy",
			edit: "Edit", undo: "Undo", redo: "Redo", cut: "Cut", copy: "Copy", paste: "Paste",
			window: "Window", minimise: "Minimize", zoom: "Zoom", closeWindow: "Close Window",
			show:        "Show Sniffy",
			description: "HTTP/HTTPS capture & proxy tool",
		}
	case "zh-Hant":
		return menuLabels{
			about: "關於 Sniffy", services: "服務", hide: "隱藏 Sniffy",
			hideOthers: "隱藏其他", showAll: "全部顯示", quit: "結束 Sniffy",
			edit: "編輯", undo: "復原", redo: "重做", cut: "剪下", copy: "拷貝", paste: "貼上",
			window: "視窗", minimise: "最小化", zoom: "縮放", closeWindow: "關閉視窗",
			show:        "顯示 Sniffy",
			description: "HTTP/HTTPS 封包擷取代理工具",
		}
	default: // zh-Hans
		return menuLabels{
			about: "关于 Sniffy", services: "服务", hide: "隐藏 Sniffy",
			hideOthers: "隐藏其他", showAll: "全部显示", quit: "退出 Sniffy",
			edit: "编辑", undo: "撤销", redo: "重做", cut: "剪切", copy: "复制", paste: "粘贴",
			window: "窗口", minimise: "最小化", zoom: "缩放", closeWindow: "关闭窗口",
			show:        "显示 Sniffy",
			description: "HTTP/HTTPS 抓包代理工具",
		}
	}
}
