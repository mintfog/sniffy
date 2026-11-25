// Copyright 2026 The mintfog Authors
// SPDX-License-Identifier: Apache-2.0
// Use of this source code is governed by an Apache 2.0
// license that can be found in the LICENSE file.

//go:build linux || darwin

package process

import (
	"io/fs"
	"slices"
	"testing"
	"time"
)

// fakeImages 是可随时改写的进程表，记录每个进程被读了几次。
type fakeImages struct {
	paths map[uint32]string
	reads map[uint32]int
}

func (f *fakeImages) read(pid uint32) (string, string, error) {
	f.reads[pid]++
	path, ok := f.paths[pid]
	if !ok {
		return "", "", fs.ErrNotExist
	}
	return path, "", nil
}

func TestProcessImagesCandidates(t *testing.T) {
	procs := &fakeImages{
		paths: map[uint32]string{1: "/usr/bin/bash", 2: "/usr/bin/curl", 3: "/opt/google/chrome/chrome"},
		reads: map[uint32]int{},
	}
	c := newProcessImages(procs.read)
	curl := NewAppSet([]string{"curl"})
	start := time.Now()

	if got := c.candidates([]uint32{1, 2, 3}, curl, start); !slices.Equal(got, []uint32{2}) {
		t.Fatalf("候选进程 %v，期望 [2]", got)
	}

	// 刚出现的进程每次都重读：fork 出来、还没 exec 的 bash 变成 curl 后立即被选中。
	procs.paths[1] = "/usr/bin/curl"
	if got := c.candidates([]uint32{1, 2, 3}, curl, start.Add(10*time.Millisecond)); !slices.Equal(got, []uint32{1, 2}) {
		t.Fatalf("exec 之后候选进程 %v，期望 [1 2]", got)
	}

	// 稳定之后在重读周期内不再重读。
	settled := start.Add(imageSettle)
	c.candidates([]uint32{1, 2, 3}, curl, settled)
	before := procs.reads[3]
	c.candidates([]uint32{1, 2, 3}, curl, settled.Add(imageRecheck/2))
	if procs.reads[3] != before {
		t.Fatalf("稳定的进程在重读周期内被重读了 %d 次", procs.reads[3]-before)
	}

	// 运行一段时间后才 exec 的进程在下个重读周期被发现。
	procs.paths[3] = "/usr/bin/curl"
	if got := c.candidates([]uint32{1, 2, 3}, curl, settled.Add(imageRecheck)); !slices.Equal(got, []uint32{1, 2, 3}) {
		t.Fatalf("重读后候选进程 %v，期望 [1 2 3]", got)
	}

	// 换了名单即按缓存重新匹配，不必重读。
	reads := procs.reads[3]
	chrome := NewAppSet([]string{"bash"})
	if got := c.candidates([]uint32{1, 2, 3}, chrome, settled.Add(imageRecheck+time.Millisecond)); len(got) != 0 {
		t.Fatalf("换名单后候选进程 %v，期望为空", got)
	}
	if procs.reads[3] != reads {
		t.Fatal("换名单不应重读稳定的进程")
	}

	// 退出的进程不再列出，记录随之删除；读的时候已经不在的直接跳过。
	delete(procs.paths, 2)
	if got := c.candidates([]uint32{1, 3, 4}, curl, settled.Add(2*imageRecheck)); !slices.Equal(got, []uint32{1, 3}) {
		t.Fatalf("候选进程 %v，期望 [1 3]", got)
	}
	if _, ok := c.entries[2]; ok {
		t.Fatal("不再列出的进程仍在缓存里")
	}
	if _, ok := c.entries[4]; ok {
		t.Fatal("读取时已经退出的进程不应留在缓存里")
	}
}
