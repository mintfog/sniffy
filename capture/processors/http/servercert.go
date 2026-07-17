// Copyright 2026 The mintfog Authors
// SPDX-License-Identifier: Apache-2.0
// Use of this source code is governed by an Apache 2.0
// license that can be found in the LICENSE file.

package http

import (
	"crypto/tls"
	"crypto/x509"
	"strings"
	"sync/atomic"
	"time"
)

// importedServerCert 绑定一张用户导入的证书与其匹配所需的预解析信息。
type importedServerCert struct {
	leaf *x509.Certificate
	cert *tls.Certificate
	// exacts 是该证书精确覆盖的主机(小写):非通配 DNS SAN、IP SAN,以及无任何 SAN 时回退的 CN。
	// 用于让精确命中优先于通配命中,并兜住 CN-only 证书(VerifyHostname 会拒绝它们)。
	exacts map[string]struct{}
}

// importedServerCertsPtr 持有当前导入的服务端证书列表(nil/空 = 无导入)。
// 由 SetImportedServerCerts 原子写入,MITM 握手在热路径上并发读取。
var importedServerCertsPtr atomic.Pointer[[]importedServerCert]

// SetImportedServerCerts 下发用户导入的服务端证书:MITM 握手命中主机时,直接把这张真实证书
// 呈给客户端(而非现签的伪造证书),使固定证书(pinning)的客户端校验通过——前提是该客户端本就
// 信任此证书链,且导入方持有对应私钥。运行时即时生效、并发安全。
//
// 主机匹配用 x509 的 RFC 6125 语义(leaf.VerifyHostname):通配 *.d 只匹配单层子域、不匹配裸域或
// 多层子域,IP SAN 亦正确处理。刻意不复用解密范围的 glob(compileHostPattern):那套 *.d→裸域+任意
// 层级的宽松语义是给用户手写 allow/deny 用的,拿来选证书会把证书呈给它并不覆盖的主机,反被客户端
// 的域名校验拒绝,比伪造证书更糟。
func SetImportedServerCerts(certs []*tls.Certificate) {
	list := make([]importedServerCert, 0, len(certs))
	for _, c := range certs {
		if c == nil || len(c.Certificate) == 0 {
			continue
		}
		leaf := c.Leaf
		if leaf == nil {
			parsed, err := x509.ParseCertificate(c.Certificate[0])
			if err != nil {
				continue
			}
			leaf = parsed
		}
		list = append(list, importedServerCert{leaf: leaf, cert: c, exacts: exactHosts(leaf)})
	}
	importedServerCertsPtr.Store(&list)
}

// exactHosts 抽取证书精确覆盖的主机(不含通配):非通配 DNS SAN + IP SAN;无任何 SAN 时回退 CN。
func exactHosts(leaf *x509.Certificate) map[string]struct{} {
	m := make(map[string]struct{}, len(leaf.DNSNames)+len(leaf.IPAddresses))
	for _, d := range leaf.DNSNames {
		if !strings.Contains(d, "*") {
			m[strings.ToLower(d)] = struct{}{}
		}
	}
	for _, ip := range leaf.IPAddresses {
		m[strings.ToLower(ip.String())] = struct{}{}
	}
	if len(leaf.DNSNames) == 0 && len(leaf.IPAddresses) == 0 {
		if cn := strings.ToLower(strings.TrimSpace(leaf.Subject.CommonName)); cn != "" {
			m[cn] = struct{}{}
		}
	}
	return m
}

// validAt 报告证书在给定时刻是否处于有效期内(含边界)。
func (ic *importedServerCert) validAt(now time.Time) bool {
	return !now.Before(ic.leaf.NotBefore) && !now.After(ic.leaf.NotAfter)
}

// importedServerCertFor 返回命中该 CONNECT 目标(host:port)的导入证书;未命中返回 nil。
// 两趟匹配:先精确 SAN/CN 命中(exact 优先于通配),再按 RFC 6125 语义(含通配)兜底;
// 从而在同时导入了 exact 与通配证书时确定地选中更精确的一张。
// 已过期/未生效的证书一律跳过(继续找下一张),使握手落回 CA 现签而非呈上一张必被客户端拒绝的
// 证书把主机弄死;有效期随时间变化,故在匹配时判断而非下发时预筛。
func importedServerCertFor(hostport string) *tls.Certificate {
	p := importedServerCertsPtr.Load()
	if p == nil || len(*p) == 0 {
		return nil
	}
	host := hostOnly(hostport)
	now := time.Now()
	for i := range *p {
		ic := &(*p)[i]
		if _, ok := ic.exacts[host]; ok && ic.validAt(now) {
			return ic.cert
		}
	}
	for i := range *p {
		ic := &(*p)[i]
		if ic.leaf.VerifyHostname(host) == nil && ic.validAt(now) {
			return ic.cert
		}
	}
	return nil
}
