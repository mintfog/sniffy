// Copyright 2026 The mintfog Authors
// SPDX-License-Identifier: Apache-2.0
// Use of this source code is governed by an Apache 2.0
// license that can be found in the LICENSE file.

//go:build linux || darwin

package process

import (
	"errors"
	"io/fs"
	"sync"
	"time"
)

const (
	// imageSettle 是新出现的进程每次查找都重读可执行文件的时长。fork 之后、exec 之前读到的是父进程
	// 的文件：shell 启动的 curl 若在这段时间里被读到，在重读之前一直会被当成 shell，而 curl 启动后
	// 几十毫秒内就会发起连接。
	imageSettle = 2 * time.Second
	// imageRecheck 是此后重读的周期，覆盖运行一段时间之后才 exec 的进程（如 exec 成别的程序的
	// 启动脚本）。
	imageRecheck = time.Second
)

// processImages 缓存各进程的可执行文件，每次查找时据此挑出名单里的进程，不必逐个重读。
type processImages struct {
	mu      sync.Mutex
	entries map[uint32]*processImage
	gen     uint64
	// read 读进程的可执行文件路径与进程名别名（见 AppSet.Match）。进程已经不在时返回的错误满足
	// errors.Is(err, fs.ErrNotExist)；读不到路径（无权查看）而读得到别名时照常返回别名。
	read func(pid uint32) (path, name string, err error)
}

type processImage struct {
	path, name string
	firstSeen  time.Time
	checked    time.Time
	// gen 是最近一次列出它的枚举。
	gen uint64
	// apps 与 matched 是上次匹配用的名单与结果，名单换了或重读之后重算。
	apps    *AppSet
	matched bool
}

func newProcessImages(read func(pid uint32) (string, string, error)) *processImages {
	return &processImages{entries: make(map[uint32]*processImage), read: read}
}

// candidates 返回 pids 中可执行文件在 apps 名单里的进程。pids 是刚枚举到的全部进程，不在其中的
// 记录随之删除。
func (c *processImages) candidates(pids []uint32, apps *AppSet, now time.Time) []uint32 {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.gen++
	var out []uint32
	for _, pid := range pids {
		e := c.entries[pid]
		if e == nil {
			e = &processImage{firstSeen: now}
			c.entries[pid] = e
		}
		e.gen = c.gen
		if e.checked.IsZero() || now.Sub(e.firstSeen) < imageSettle || now.Sub(e.checked) >= imageRecheck {
			path, name, err := c.read(pid)
			if errors.Is(err, fs.ErrNotExist) {
				delete(c.entries, pid)
				continue
			}
			if path != e.path || name != e.name {
				e.path, e.name, e.apps = path, name, nil
			}
			e.checked = now
		}
		if e.apps != apps {
			e.apps, e.matched = apps, apps.Match(e.path, e.name)
		}
		if e.matched {
			out = append(out, pid)
		}
	}
	for pid, e := range c.entries {
		if e.gen != c.gen {
			delete(c.entries, pid)
		}
	}
	return out
}
