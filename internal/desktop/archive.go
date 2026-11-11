// Copyright 2026 The mintfog Authors
// SPDX-License-Identifier: Apache-2.0
// Use of this source code is governed by an Apache 2.0
// license that can be found in the LICENSE file.

//go:build desktop

package desktop

import (
	"context"
	"io"
	"os"
	"path/filepath"
	"time"

	"github.com/mintfog/sniffy/internal/service"
	"github.com/wailsapp/wails/v3/pkg/application"
)

// ExportSessions 以 HAR 1.2 导出，返回脱敏统计；取消文件选择时返回 nil。
func (b *Bridge) ExportSessions(ids []string, sanitize service.SanitizeOptions) (*service.SanitizeStats, error) {
	app := application.Get()
	if app == nil {
		return nil, nil
	}
	dlg := app.Dialog.SaveFile()
	dlg.SetFilename("sniffy-" + time.Now().Format("20060102-150405") + ".har")
	dlg.AddFilter("HAR", "*.har")
	if w := app.Window.Current(); w != nil {
		dlg.AttachToWindow(w)
	}
	dest, err := dlg.PromptForSingleSelection()
	if err != nil {
		return nil, err
	}
	if dest == "" {
		return nil, nil
	}

	// 先完成临时文件，正文读取或写盘失败时保留用户原有文件。
	file, err := os.CreateTemp(filepath.Dir(dest), ".sniffy-export-*")
	if err != nil {
		return nil, err
	}
	defer os.Remove(file.Name())
	stats, err := b.app.Service.WriteSessionArchive(context.Background(), file, ids, service.ArchiveOptions{
		IncludeRequestBody:  true,
		IncludeResponseBody: true,
		Sanitize:            sanitize,
	})
	closeErr := file.Close()
	if err != nil {
		return nil, err
	}
	if closeErr != nil {
		return nil, closeErr
	}
	if err := os.Rename(file.Name(), dest); err != nil {
		return nil, err
	}
	return &stats, nil
}

// ImportSessions 返回 nil 表示取消。
func (b *Bridge) ImportSessions() (*service.SessionImportResult, error) {
	app := application.Get()
	if app == nil {
		return nil, nil
	}
	dlg := app.Dialog.OpenFile()
	dlg.SetTitle("导入 HAR 会话")
	dlg.AddFilter("HAR", "*.har;*.json")
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
	info, err := file.Stat()
	if err != nil {
		return nil, err
	}
	total := info.Size()
	result, err := b.app.Service.ImportSessionArchive(&importProgressReader{
		r:    file,
		last: time.Now(),
		report: func(read int64) {
			app.Event.Emit(sessionImportProgressEvent, importProgress{Read: read, Total: total})
		},
	})
	if err != nil {
		return nil, err
	}
	return &result, nil
}

const (
	sessionImportProgressEvent = "session_import_progress"
	importProgressInterval     = 100 * time.Millisecond
)

type importProgress struct {
	Read  int64 `json:"read"`
	Total int64 `json:"total"`
}

// importProgressReader 按已读字节数上报导入进度，两次上报至少间隔 importProgressInterval；
// last 初始为导入开始时刻，耗时短于该间隔的导入不产生事件。
type importProgressReader struct {
	r      io.Reader
	read   int64
	last   time.Time
	report func(read int64)
}

func (p *importProgressReader) Read(b []byte) (int, error) {
	n, err := p.r.Read(b)
	p.read += int64(n)
	if now := time.Now(); now.Sub(p.last) >= importProgressInterval {
		p.last = now
		p.report(p.read)
	}
	return n, err
}
