// Copyright 2026 The mintfog Authors
// SPDX-License-Identifier: Apache-2.0
// Use of this source code is governed by an Apache 2.0
// license that can be found in the LICENSE file.

//go:build desktop && darwin

package desktop

/*
#cgo CFLAGS: -x objective-c
#cgo LDFLAGS: -framework Cocoa

#include <stdlib.h>
#include <string.h>
#import <Cocoa/Cocoa.h>

// AppKit 会按 undo:/paste: 等标准 selector 识别出“编辑”菜单（与菜单标题无关），并在装主菜单时
// 向其尾部自动追加系统项：听写、表情与符号，新系统还有自动填充、写作工具——应用未本地化时这些项
// 一律是英文。注册 NSDisabled* 默认值可阻止追加（Chromium 同款做法；registerDefaults 只写进程
// 内存，不落用户磁盘配置）。后两个 key 未见官方文档，对不识别的 key 系统会忽略，无副作用。
static void sniffySuppressAutomaticMenuItems(void) {
	@autoreleasepool {
		[[NSUserDefaults standardUserDefaults] registerDefaults:@{
			@"NSDisabledDictationMenuItem" : @YES,
			@"NSDisabledCharacterPaletteMenuItem" : @YES,
			@"NSDisabledWritingToolsMenuItem" : @YES,
			@"NSDisabledAutoFillMenuItem" : @YES,
		}];
	}
}

// 把主菜单里标题为 title 的子菜单修剪到 keep 个子项。我们自建的项都在前面，系统自动追加的
// 都接在尾部，多出来的一律移除。异步派发到主线程，保证跑在 setMainMenu（及其自动追加）之后。
static void sniffyPruneMenuTail(const char* title, int keep) {
	char *t = strdup(title);
	dispatch_async(dispatch_get_main_queue(), ^{
		@autoreleasepool {
			NSString *name = [NSString stringWithUTF8String:t];
			for (NSMenuItem *top in [[NSApp mainMenu] itemArray]) {
				if (![[top title] isEqualToString:name]) continue;
				NSMenu *sub = [top submenu];
				if (sub == nil) continue;
				while ((int)[sub numberOfItems] > keep) {
					[sub removeItemAtIndex:[sub numberOfItems] - 1];
				}
			}
		}
		free(t);
	});
}

// sniffyPreferredLanguage 返回用户偏好语言列表的首项（BCP-47，如 "zh-Hans-CN" / "zh-Hant-TW" / "en-US"）。
// 返回的 C 字符串由调用方 free。GUI 应用（Finder 启动）不继承 shell 的 LANG/LC_*，故必须走 NSLocale。
static const char* sniffyPreferredLanguage(void) {
	@autoreleasepool {
		NSArray<NSString *> *langs = [NSLocale preferredLanguages];
		if ([langs count] == 0) return NULL;
		return strdup([[langs objectAtIndex:0] UTF8String]);
	}
}
*/
import "C"

import "unsafe"

// suppressAutomaticMenuItems 阻止 AppKit 向“编辑”菜单自动追加英文系统项
// （听写/表情与符号/自动填充/写作工具）。须在主菜单首次安装（wapp.Run）前调用。
func suppressAutomaticMenuItems() {
	C.sniffySuppressAutomaticMenuItems()
}

// pruneMenuTail 把主菜单里标题为 title 的子菜单修剪到 keep 个项，
// 兜底清掉 NSDisabled* 默认值没拦住的系统自动追加项（见 suppressAutomaticMenuItems）。
func pruneMenuTail(title string, keep int) {
	ct := C.CString(title)
	defer C.free(unsafe.Pointer(ct))
	C.sniffyPruneMenuTail(ct, C.int(keep))
}

// osPreferredLang 取 macOS 用户偏好语言首项（见 locale.go），用于启动期占位 UI。
func osPreferredLang() string {
	c := C.sniffyPreferredLanguage()
	if c == nil {
		return ""
	}
	defer C.free(unsafe.Pointer(c))
	return C.GoString(c)
}
