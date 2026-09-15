// Copyright 2026 The mintfog Authors
// SPDX-License-Identifier: Apache-2.0
// Use of this source code is governed by an Apache 2.0
// license that can be found in the LICENSE file.

package websocket

import (
	"sync/atomic"

	"github.com/mintfog/sniffy/internal/outboundtls"
)

var outboundTLSPolicy atomic.Pointer[outboundtls.Policy]

// SetOutboundTLSPolicy 允许握手与配置更新并发执行；nil 恢复默认的证书验证。
func SetOutboundTLSPolicy(p *outboundtls.Policy) {
	outboundTLSPolicy.Store(p)
}
