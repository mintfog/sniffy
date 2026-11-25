// Copyright 2026 The mintfog Authors
// SPDX-License-Identifier: Apache-2.0
// Use of this source code is governed by an Apache 2.0
// license that can be found in the LICENSE file.

//go:build darwin

package process

import (
	"encoding/binary"
	"errors"
	"io/fs"
	"net/netip"
	"time"

	"golang.org/x/sys/unix"
)

type ownerFinder struct {
	api    *darwinLibproc
	images *processImages
	scans  *ownerScans[darwinConnectionKey]
}

func newOwnerFinder() (ownerFinder, error) {
	api, err := loadDarwinLibproc()
	if err != nil {
		return ownerFinder{}, err
	}
	read := func(pid uint32) (string, string, error) {
		path := api.path(int32(pid))
		// 无权查看的进程读不到路径，kill(pid, 0) 报 EPERM；ESRCH 才是进程已经不在。
		if path == "" && errors.Is(unix.Kill(int(pid), 0), unix.ESRCH) {
			return "", "", fs.ErrNotExist
		}
		return path, "", nil
	}
	return ownerFinder{api: api, images: newProcessImages(read), scans: new(ownerScans[darwinConnectionKey])}, nil
}

func (f *ownerFinder) find(local, remote netip.AddrPort, apps *AppSet) (uint32, bool, error) {
	key := darwinConnectionKey{local: local, remote: remote}
	return f.scans.find(time.Now(), apps,
		func() (darwinConnectionKey, error) { return key, nil },
		func() (map[darwinConnectionKey]uint32, error) { return f.scan(apps) })
}

// scan 记下名单里的进程所持的全部 TCP 连接，名单里没有进程在运行时返回 nil。
func (f *ownerFinder) scan(apps *AppSet) (map[darwinConnectionKey]uint32, error) {
	all, err := f.api.readPIDs()
	if err != nil {
		return nil, err
	}
	pids := make([]uint32, 0, len(all))
	for _, pid := range all {
		if pid > 0 {
			pids = append(pids, uint32(pid))
		}
	}
	candidates := f.images.candidates(pids, apps, time.Now())
	if len(candidates) == 0 {
		return nil, nil
	}
	owners := make(map[darwinConnectionKey]uint32)
	fdBuffer := make([]byte, 256*darwinFDSize)
	var socketBuffer [darwinSocketSize]byte
	for _, pid := range candidates {
		fdBuffer = f.api.readFDs(int32(pid), fdBuffer)
		for offset := 0; offset < len(fdBuffer); offset += darwinFDSize {
			if binary.LittleEndian.Uint32(fdBuffer[offset+4:]) != procFDSocket {
				continue
			}
			fd := int32(binary.LittleEndian.Uint32(fdBuffer[offset:]))
			if f.api.fdInfo(int32(pid), fd, procPIDFDSocketInfo, &socketBuffer[0], darwinSocketSize) != darwinSocketSize {
				continue
			}
			if conn, ok := parseDarwinSocket(socketBuffer[:]); ok {
				owners[conn.darwinConnectionKey] = pid
			}
		}
	}
	return owners, nil
}
