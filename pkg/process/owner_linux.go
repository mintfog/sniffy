// Copyright 2026 The mintfog Authors
// SPDX-License-Identifier: Apache-2.0
// Use of this source code is governed by an Apache 2.0
// license that can be found in the LICENSE file.

//go:build linux

package process

import (
	"bytes"
	"errors"
	"fmt"
	"io/fs"
	"net/netip"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"golang.org/x/sys/unix"
)

type ownerFinder struct {
	procRoot string
	images   *processImages
	scans    *ownerScans[uint64]
}

func newOwnerFinder() (ownerFinder, error) {
	f := ownerFinder{procRoot: "/proc", scans: new(ownerScans[uint64])}
	f.images = newProcessImages(f.readImage)
	return f, nil
}

// find 在名单里的进程所持的 socket 里找这条连接的 inode。名单里没有进程在运行时不必查连接本身。
func (f *ownerFinder) find(local, remote netip.AddrPort, apps *AppSet) (uint32, bool, error) {
	return f.scans.find(time.Now(), apps,
		func() (uint64, error) { return f.socketInode(local, remote) },
		func() (map[uint64]uint32, error) { return f.scan(apps) })
}

// scan 记下名单里的进程所持的全部 socket 的 inode，名单里没有进程在运行时返回 nil。
func (f *ownerFinder) scan(apps *AppSet) (map[uint64]uint32, error) {
	pids, err := f.listPIDs()
	if err != nil {
		return nil, err
	}
	candidates := f.images.candidates(pids, apps, time.Now())
	if len(candidates) == 0 {
		return nil, nil
	}
	owners := make(map[uint64]uint32)
	for _, pid := range candidates {
		f.collectSockets(pid, owners)
	}
	return owners, nil
}

// socketInode 取连接的 socket inode。sock_diag 出错时改读 /proc/net/tcp：内核没有 inet_diag 模块时
// sock_diag 报的 ENOENT 与查不到连接分不开。
func (f *ownerFinder) socketInode(local, remote netip.AddrPort) (uint64, error) {
	if inode, err := sockDiagInode(local, remote); err == nil {
		return inode, nil
	}
	return f.procfsInode(local, remote)
}

func (f *ownerFinder) procfsInode(local, remote netip.AddrPort) (uint64, error) {
	d := LinuxDetector{procRoot: f.procRoot}
	inode, err := d.findConnectionInode(local, remote)
	if err != nil {
		return 0, err
	}
	return strconv.ParseUint(inode, 10, 64)
}

func (f *ownerFinder) listPIDs() ([]uint32, error) {
	dir, err := os.Open(f.procRoot)
	if err != nil {
		return nil, err
	}
	defer dir.Close()
	names, err := dir.Readdirnames(-1)
	if err != nil {
		return nil, fmt.Errorf("枚举进程: %w", err)
	}
	pids := make([]uint32, 0, len(names))
	for _, name := range names {
		if pid, err := strconv.ParseUint(name, 10, 32); err == nil && pid != 0 {
			pids = append(pids, uint32(pid))
		}
	}
	return pids, nil
}

func runningImages() ([]runningImage, error) {
	f := ownerFinder{procRoot: "/proc"}
	pids, err := f.listPIDs()
	if err != nil {
		return nil, err
	}
	images := make([]runningImage, 0, len(pids))
	for _, pid := range pids {
		if path, name, err := f.readImage(pid); err == nil {
			images = append(images, runningImage{path: path, name: name})
		}
	}
	return images, nil
}

// readImage 读进程的可执行文件与 comm。可执行文件无权查看（其它用户的进程）或不存在（内核线程）
// 时只返回 comm。
func (f *ownerFinder) readImage(pid uint32) (string, string, error) {
	dir := filepath.Join(f.procRoot, strconv.FormatUint(uint64(pid), 10))
	path, exeErr := readExe(dir)
	comm, commErr := os.ReadFile(filepath.Join(dir, "comm"))
	if exeErr != nil && commErr != nil {
		if errors.Is(commErr, fs.ErrNotExist) {
			return "", "", commErr
		}
		return "", "", nil
	}
	return path, strings.TrimSuffix(string(comm), "\n"), nil
}

var socketLinkPrefix = []byte("socket:[")

// collectSockets 把进程打开的 socket 的 inode 记入 owners。
func (f *ownerFinder) collectSockets(pid uint32, owners map[uint64]uint32) {
	dir, err := os.Open(filepath.Join(f.procRoot, strconv.FormatUint(uint64(pid), 10), "fd"))
	if err != nil {
		return
	}
	defer dir.Close()
	names, _ := dir.Readdirnames(-1)
	dirfd := int(dir.Fd())
	var buf [64]byte
	for _, name := range names {
		n, err := unix.Readlinkat(dirfd, name, buf[:])
		if err != nil {
			continue
		}
		// 链接形如 socket:[12345]。
		link := buf[:n]
		if !bytes.HasPrefix(link, socketLinkPrefix) || link[n-1] != ']' {
			continue
		}
		if inode, err := strconv.ParseUint(string(link[len(socketLinkPrefix):n-1]), 10, 64); err == nil {
			owners[inode] = pid
		}
	}
}
