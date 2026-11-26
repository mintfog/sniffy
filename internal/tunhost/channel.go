// Copyright 2026 The mintfog Authors
// SPDX-License-Identifier: Apache-2.0
// Use of this source code is governed by an Apache 2.0
// license that can be found in the LICENSE file.

package tunhost

import (
	"errors"
	"fmt"
	"io/fs"
	"net"
	"os"
	"path/filepath"
	"sync"

	"github.com/mintfog/sniffy/internal/platform"
)

// channel 是主进程监听的通道：系统临时目录下新建的目录里的一个 Unix socket，只有当前用户与提权后
// 的 helper 能访问。
type channel struct {
	mu     sync.Mutex
	dir    string
	ln     *net.UnixListener
	closed bool
}

func newChannel() (*channel, error) {
	// helper 的工作目录与本进程不同（pkexec 会切换），临时目录是相对路径时两端会解析到不同位置。
	tmp, err := filepath.Abs(os.TempDir())
	if err != nil {
		return nil, fmt.Errorf("定位临时目录: %w", err)
	}
	dir, err := os.MkdirTemp(tmp, "sniffy-tun-")
	if err != nil {
		return nil, fmt.Errorf("创建通道目录: %w", err)
	}
	c := &channel{dir: dir}
	if err := c.restrictDir(); err != nil {
		_ = os.RemoveAll(dir)
		return nil, err
	}
	if c.ln, err = net.ListenUnix("unix", &net.UnixAddr{Name: c.path(), Net: "unix"}); err != nil {
		_ = os.RemoveAll(dir)
		return nil, fmt.Errorf("创建通道: %w", err)
	}
	return c, nil
}

// restrictDir 收紧刚建好的 dir 的权限。Windows 上新目录继承 TEMP 的权限，TEMP 指向共享目录时别的
// 用户也能连入通道、改动其中的 socket。只授权当前用户与提权后的 helper，后者可能属于另一个管理员
// 账户。
func (c *channel) restrictDir() error {
	if err := platform.RestrictToOwnerAndAdmins(c.dir); err != nil {
		return fmt.Errorf("收紧通道目录权限: %w", err)
	}
	return nil
}

func (c *channel) path() string { return filepath.Join(c.dir, "ch") }

// repair 在通道文件不见了（如被临时目录的定期清理删掉）时就地重建，返回新的监听器，旧的随之关闭；
// 通道文件还在、或通道已关闭时返回 nil。helper 按原路径拨交接连接，文件没了就全部失败。
//
// 目录一起被删了就重新建，原处已有目录就直接在里面重建。即使那个目录已被别的用户换掉（TEMP 是共享
// 目录时），helper 每条交接连接都核对对端是不是本进程，换掉的人拿不到连接。
func (c *channel) repair() (*net.UnixListener, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.closed {
		return nil, nil
	}
	// Lstat 出任何错都算通道不在了：目录被换成普通文件时报的不是「不存在」。
	if _, err := os.Lstat(c.path()); err == nil {
		return nil, nil
	}
	switch err := os.Mkdir(c.dir, 0o700); {
	case err == nil:
		if err := c.restrictDir(); err != nil {
			_ = os.Remove(c.dir)
			return nil, err
		}
	case !errors.Is(err, fs.ErrExist):
		return nil, fmt.Errorf("重建通道目录: %w", err)
	}
	ln, err := net.ListenUnix("unix", &net.UnixAddr{Name: c.path(), Net: "unix"})
	if err != nil {
		return nil, fmt.Errorf("重建通道: %w", err)
	}
	// 旧监听器的文件已经不在了，同一路径上现在是新的 socket，关闭时不能再按路径删除。
	c.ln.SetUnlinkOnClose(false)
	_ = c.ln.Close()
	c.ln = ln
	return ln, nil
}

// close 停止监听并删除通道目录，可重复调用。
func (c *channel) close() {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.closed {
		return
	}
	c.closed = true
	_ = c.ln.Close()
	_ = os.RemoveAll(c.dir)
}
