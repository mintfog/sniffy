// Copyright 2026 The mintfog Authors
// SPDX-License-Identifier: Apache-2.0
// Use of this source code is governed by an Apache 2.0
// license that can be found in the LICENSE file.

//go:build desktop

package desktop

import (
	"context"
	"os"
	"path/filepath"
	"time"

	"github.com/mintfog/sniffy/internal/service"
	"github.com/wailsapp/wails/v3/pkg/application"
)

// ExportSessions 以 HAR 1.2 导出，返回 true 表示已保存；取消文件选择时返回 false。
func (b *Bridge) ExportSessions(ids []string) (bool, error) {
	app := application.Get()
	if app == nil {
		return false, nil
	}
	dlg := app.Dialog.SaveFile()
	dlg.SetFilename("sniffy-" + time.Now().Format("20060102-150405") + ".har")
	dlg.AddFilter("HAR", "*.har")
	if w := app.Window.Current(); w != nil {
		dlg.AttachToWindow(w)
	}
	dest, err := dlg.PromptForSingleSelection()
	if err != nil {
		return false, err
	}
	if dest == "" {
		return false, nil
	}

	// 先完成临时文件，正文读取或写盘失败时保留用户原有文件。
	file, err := os.CreateTemp(filepath.Dir(dest), ".sniffy-export-*")
	if err != nil {
		return false, err
	}
	defer os.Remove(file.Name())
	err = b.app.Service.WriteSessionArchive(context.Background(), file, ids, true, true)
	closeErr := file.Close()
	if err != nil {
		return false, err
	}
	if closeErr != nil {
		return false, closeErr
	}
	if err := os.Rename(file.Name(), dest); err != nil {
		return false, err
	}
	return true, nil
}

// ImportSessions 返回 nil 表示取消；成功时返回实际解析的会话数量。
func (b *Bridge) ImportSessions() (*service.SessionImportResult, error) {
	app := application.Get()
	if app == nil {
		return nil, nil
	}
	dlg := app.Dialog.OpenFile()
	dlg.SetTitle("导入 HAR/JSON 会话")
	dlg.AddFilter("HAR/JSON", "*.har;*.json")
	if w := app.Window.Current(); w != nil {
		dlg.AttachToWindow(w)
	}
	path, err := dlg.PromptForSingleSelection()
	if err != nil {
		return nil, err
	}
	if path == "" {
		return nil, nil
	}

	file, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer file.Close()
	result, err := b.app.Service.ImportSessionArchive(file)
	if err != nil {
		return nil, err
	}
	return &result, nil
}
