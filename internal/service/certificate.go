// Copyright 2026 The mintfog Authors
// SPDX-License-Identifier: Apache-2.0
// Use of this source code is governed by an Apache 2.0
// license that can be found in the LICENSE file.

package service

import (
	"encoding/pem"
	"sync"

	"github.com/mintfog/sniffy/ca"
)

// certStore 包装引擎持有的 CA,提供真实的证书导出能力
// (替代历史上 web_api 返回字面量 "CA CERTIFICATE DATA" 的桩实现)。
type certStore struct {
	mu sync.RWMutex
	ca ca.CA
}

func newCertStore(c ca.CA) *certStore {
	return &certStore{ca: c}
}

// setCA 替换底层 CA(重新生成 CA 后调用),并发安全。
func (cs *certStore) setCA(c ca.CA) {
	cs.mu.Lock()
	cs.ca = c
	cs.mu.Unlock()
}

// ExportPEM 返回根 CA 证书的 PEM 编码,用于客户端安装。
func (cs *certStore) ExportPEM() []byte {
	cs.mu.RLock()
	c := cs.ca
	cs.mu.RUnlock()
	if c == nil {
		return nil
	}
	caCert := c.GetCA()
	if caCert == nil {
		return nil
	}
	return pem.EncodeToMemory(&pem.Block{
		Type:  "CERTIFICATE",
		Bytes: caCert.Raw,
	})
}

// ExportMobileconfig 返回内嵌根证书的 iOS 配置描述文件(.mobileconfig),
// 供 iOS 客户端经 Safari 下载安装;CA 不可用时返回 nil。
func (cs *certStore) ExportMobileconfig() []byte {
	cs.mu.RLock()
	c := cs.ca
	cs.mu.RUnlock()
	if c == nil {
		return nil
	}
	return ca.Mobileconfig(c.GetCA())
}
