// Copyright 2026 The mintfog Authors
// SPDX-License-Identifier: Apache-2.0
// Use of this source code is governed by an Apache 2.0
// license that can be found in the LICENSE file.

//go:build linux || darwin

package process

import (
	"errors"
	"sync"
	"testing"
	"time"
)

func TestOwnerScansReuse(t *testing.T) {
	var s ownerScans[int]
	apps := NewAppSet([]string{"curl"})
	scans := 0
	scan := func() (map[int]uint32, error) {
		scans++
		return map[int]uint32{1: 100}, nil
	}
	key := func(k int) func() (int, error) { return func() (int, error) { return k, nil } }

	begin := time.Now()
	if pid, ok, err := s.find(begin, apps, key(1), scan); pid != 100 || !ok || err != nil || scans != 1 {
		t.Fatalf("首次查找 = (%d, %v, %v)，扫描 %d 轮", pid, ok, err, scans)
	}
	// 上一轮在这次查找开始之后才开始：共用。
	if _, ok, _ := s.find(begin, apps, key(2), scan); ok || scans != 1 {
		t.Fatalf("应共用上一轮，扫描了 %d 轮、结果 %v", scans, ok)
	}
	// 上一轮在这次查找开始之前就开始了，可能看不到这条连接：重扫。
	_, _, _ = s.find(time.Now(), apps, key(1), scan)
	if scans != 2 {
		t.Fatalf("应重扫，扫描了 %d 轮", scans)
	}
	// 名单换了：重扫。
	_, _, _ = s.find(begin, NewAppSet([]string{"curl"}), key(1), scan)
	if scans != 3 {
		t.Fatalf("换名单后应重扫，扫描了 %d 轮", scans)
	}
}

func TestOwnerScansNoCandidates(t *testing.T) {
	var s ownerScans[int]
	noKey := func() (int, error) {
		t.Error("名单里的程序都没在运行时不应查连接")
		return 0, nil
	}
	if pid, ok, err := s.find(time.Now(), NewAppSet([]string{"curl"}), noKey, func() (map[int]uint32, error) { return nil, nil }); pid != 0 || ok || err != nil {
		t.Fatalf("find = (%d, %v, %v)", pid, ok, err)
	}
}

func TestOwnerScansErrors(t *testing.T) {
	var s ownerScans[int]
	apps := NewAppSet([]string{"curl"})
	scanErr, keyErr := errors.New("扫描失败"), errors.New("查不到连接")
	if _, ok, err := s.find(time.Now(), apps, nil, func() (map[int]uint32, error) { return nil, scanErr }); ok || !errors.Is(err, scanErr) {
		t.Fatalf("扫描失败: (%v, %v)", ok, err)
	}
	if s.last != nil {
		t.Fatal("失败的扫描不应留作后续查找的结果")
	}
	found := func() (map[int]uint32, error) { return map[int]uint32{1: 100}, nil }
	if _, ok, err := s.find(time.Now(), apps, func() (int, error) { return 0, keyErr }, found); ok || !errors.Is(err, keyErr) {
		t.Fatalf("查不到连接: (%v, %v)", ok, err)
	}
}

// TestOwnerScansCoalesce：一轮扫描进行中到达的查找等它结束后，至多再扫一轮。
func TestOwnerScansCoalesce(t *testing.T) {
	var s ownerScans[int]
	apps := NewAppSet([]string{"curl"})
	var mu sync.Mutex
	scans := 0
	release := make(chan struct{})
	scan := func() (map[int]uint32, error) {
		mu.Lock()
		scans++
		first := scans == 1
		mu.Unlock()
		if first {
			<-release
		}
		return map[int]uint32{}, nil
	}
	key := func() (int, error) { return 1, nil }

	var wg sync.WaitGroup
	wg.Go(func() { _, _, _ = s.find(time.Now(), apps, key, scan) })
	for {
		mu.Lock()
		started := scans == 1
		mu.Unlock()
		if started {
			break
		}
		time.Sleep(time.Millisecond)
	}
	for range 20 {
		wg.Go(func() { _, _, _ = s.find(time.Now(), apps, key, scan) })
	}
	time.Sleep(20 * time.Millisecond)
	close(release)
	wg.Wait()
	if scans != 2 {
		t.Fatalf("21 次查找扫描了 %d 轮，期望 2 轮", scans)
	}
}
