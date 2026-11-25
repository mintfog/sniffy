// Copyright 2026 The mintfog Authors
// SPDX-License-Identifier: Apache-2.0
// Use of this source code is governed by an Apache 2.0
// license that can be found in the LICENSE file.

//go:build linux || darwin

package process

import (
	"sync"
	"time"
)

// ownerScans 让并发的查找共用同一轮扫描。一轮扫描查看名单里全部进程所持的连接，记下每条连接的持有者，
// 开销随这些进程打开的文件数增长（大型多进程应用有几千个），却与要找的是哪条连接无关。
//
// 要找的连接在查找开始之前就已建立：在此之后才开始的一轮扫描必然看得到它，结果可以直接用。连接成批
// 到达时（打开网页）每批因此最多扫两轮：一轮进行中到达的查找等它结束，再由其中一个开始新的一轮，其余的
// 共用这一轮。K 是平台标识连接的方式：Linux 上是 socket inode，macOS 上是四元组。
type ownerScans[K comparable] struct {
	mu   sync.Mutex
	last *ownerScan[K]
}

type ownerScan[K comparable] struct {
	apps    *AppSet
	started time.Time
	// owners 是名单里的进程所持的连接；为 nil 表示名单里的程序都没在运行。
	owners map[K]uint32
}

// find 找 begin 时刻之前就已建立的连接在 apps 名单里的持有者。key 取要找的连接的标识，只在名单里有
// 进程在运行时才调用（Linux 上要多一次系统调用）；scan 做一轮扫描，名单里没有进程在运行时返回 nil。
func (s *ownerScans[K]) find(begin time.Time, apps *AppSet, key func() (K, error), scan func() (map[K]uint32, error)) (uint32, bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	last := s.last
	if last == nil || last.apps != apps || last.started.Before(begin) {
		started := time.Now()
		owners, err := scan()
		if err != nil {
			return 0, false, err
		}
		last = &ownerScan[K]{apps: apps, started: started, owners: owners}
		s.last = last
	}
	if last.owners == nil {
		return 0, false, nil
	}
	k, err := key()
	if err != nil {
		return 0, false, err
	}
	pid, ok := last.owners[k]
	return pid, ok, nil
}
