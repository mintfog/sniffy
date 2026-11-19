// Copyright 2026 The mintfog Authors
// SPDX-License-Identifier: Apache-2.0
// Use of this source code is governed by an Apache 2.0
// license that can be found in the LICENSE file.

//go:build !linux

package truststore

import "crypto/x509"

// systemRoots 返回 nil，即用 crypto/x509 的系统根证书：Windows、macOS 上每次校验都直接问系统，
// 看得见运行中才装入的根证书。设置了 SSL_CERT_FILE / SSL_CERT_DIR 时例外：crypto/x509 按它们改读
// 磁盘上的证书文件，只读一次并缓存，之后的变动要重启才看得见。
func systemRoots() (*x509.CertPool, error) { return nil, nil }
