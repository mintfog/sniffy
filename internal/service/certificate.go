// Copyright 2026 The mintfog Authors
// SPDX-License-Identifier: Apache-2.0
// Use of this source code is governed by an Apache 2.0
// license that can be found in the LICENSE file.

package service

import (
	"errors"
	"strings"
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
	return ca.ExportRootCertPEM(c)
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

// ExportAs 按用户选择的格式导出根证书(或证书 + 私钥)。
// - pem/crt: 单一 PEM 编码证书,内容相同,仅扩展名不同
// - der: 裸 DER 字节
// - p12/pfx: PKCS#12 打包证书 + 私钥,需口令
// - bundle: 证书 + 私钥的联合 PEM(供 curl/nginx 等一次性载入)
// 返回 (文件字节, MIME 提示; 目前只做建议,不参与保存).
func (cs *certStore) ExportAs(format, password string) ([]byte, string, error) {
	cs.mu.RLock()
	c := cs.ca
	cs.mu.RUnlock()
	if c == nil {
		return nil, "", errors.New("根证书尚未就绪")
	}
	switch strings.ToLower(strings.TrimSpace(format)) {
	case "", ca.FormatPEM, ca.FormatCRT, "cer":
		data := ca.ExportRootCertPEM(c)
		if data == nil {
			return nil, "", errors.New("根证书导出失败")
		}
		return data, "application/x-pem-file", nil
	case ca.FormatDER:
		data := ca.ExportRootCertDER(c)
		if data == nil {
			return nil, "", errors.New("根证书导出失败")
		}
		return data, "application/x-x509-ca-cert", nil
	case "pfx", ca.FormatP12:
		data, err := ca.ExportRootPKCS12(c, password)
		if err != nil {
			return nil, "", err
		}
		return data, "application/x-pkcs12", nil
	case ca.FormatBundle, "pem-bundle":
		data, err := ca.ExportRootBundlePEM(c)
		if err != nil {
			return nil, "", err
		}
		return data, "application/x-pem-file", nil
	default:
		return nil, "", errors.New("不支持的导出格式: " + format)
	}
}
