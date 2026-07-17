// Copyright 2026 The mintfog Authors
// SPDX-License-Identifier: Apache-2.0
// Use of this source code is governed by an Apache 2.0
// license that can be found in the LICENSE file.

package service

import (
	"crypto/sha256"
	"crypto/tls"
	"crypto/x509"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"sort"
	"strings"
	"sync"
	"time"
)

// serverCertFileName 是导入的服务端证书的持久化文件名(含私钥,单独存放而非塞进
// config.json:config.json 会随前端偏好来回同步且以 0644 落盘,不宜承载私钥)。
const serverCertFileName = "servercerts.json"

// ServerCert 是一条导入的服务端证书(证书 + 私钥的 PEM),用于替代 MITM 现签的伪造证书。
// 匹配的域名不落盘:每次从证书的 SAN/CN 派生,避免与证书内容不一致。
type ServerCert struct {
	CertPEM string `json:"certPEM"`
	KeyPEM  string `json:"keyPEM"`
}

// ServerCertDTO 是暴露给前端/传输层的摘要,只含证书公开信息,绝不含私钥。
type ServerCertDTO struct {
	ID       string   `json:"id"`    // 证书指纹(SHA-256 hex),删除时按它引用
	Hosts    []string `json:"hosts"` // 从证书 SAN(无 SAN 时回退 CN)提取的匹配域名
	Subject  string   `json:"subject"`
	Issuer   string   `json:"issuer"`
	NotAfter string   `json:"notAfter"`
}

type serverCertStore struct {
	mu    sync.RWMutex
	items []ServerCert
	path  string // 持久化文件;为空则仅内存
}

func newServerCertStore(path string) *serverCertStore {
	cs := &serverCertStore{path: path}
	cs.load()
	return cs
}

func (cs *serverCertStore) load() {
	if cs.path == "" {
		return
	}
	data, err := os.ReadFile(cs.path)
	if err != nil {
		return
	}
	var items []ServerCert
	if json.Unmarshal(data, &items) != nil {
		return
	}
	cs.items = items
}

// save 原子落盘:先写同目录的 temp 文件(0600),再 rename 覆盖目标。文件含私钥,原子写避免写到
// 一半崩溃损坏文件、丢失全部导入的密钥;rename 一并把最终权限固定为 0600,不受既有文件旧权限影响。
// 错误上抛由调用方决定处理(导入需据此判断是否真的落盘)。
func (cs *serverCertStore) save() error {
	if cs.path == "" {
		return nil
	}
	data, err := json.MarshalIndent(cs.items, "", "  ")
	if err != nil {
		return err
	}
	tmp := cs.path + ".tmp"
	if err := os.WriteFile(tmp, data, 0o600); err != nil {
		return err
	}
	if err := os.Rename(tmp, cs.path); err != nil {
		_ = os.Remove(tmp)
		return err
	}
	return nil
}

// importCert 校验证书与私钥成对且匹配,从证书自身提取匹配域名后 upsert,持久化并返回摘要。
// 覆盖域名集合完全相同的旧记录(续期/替换语义);证书不含任何可用域名时返回错误。
func (cs *serverCertStore) importCert(certPEM, keyPEM string) (ServerCertDTO, error) {
	_, leaf, ok := parseEntry(ServerCert{CertPEM: certPEM, KeyPEM: keyPEM})
	if !ok {
		return ServerCertDTO{}, errors.New("证书与私钥无效或不匹配")
	}
	hosts := certHosts(leaf)
	if len(hosts) == 0 {
		return ServerCertDTO{}, errors.New("证书未包含可用于匹配的域名(SAN 或 CN)")
	}
	entry := ServerCert{CertPEM: certPEM, KeyPEM: keyPEM}
	hostKey := hostSetKey(hosts)

	cs.mu.Lock()
	prev := cs.items
	kept := make([]ServerCert, 0, len(cs.items)+1)
	for _, it := range cs.items {
		if _, lf, ok := parseEntry(it); ok && hostSetKey(certHosts(lf)) == hostKey {
			continue // 同一组域名的旧证书被新证书替换(续期语义,与 SAN 顺序无关)
		}
		kept = append(kept, it)
	}
	cs.items = append(kept, entry)
	if err := cs.save(); err != nil {
		cs.items = prev // 落盘失败:回滚内存,避免"当次生效、重启即失"的假成功
		cs.mu.Unlock()
		return ServerCertDTO{}, fmt.Errorf("保存失败: %w", err)
	}
	cs.mu.Unlock()
	return dtoFromLeaf(leaf), nil
}

// hostSetKey 把一组域名规整为顺序无关的比较键:CA 续期时可能重排 SAN 顺序,按集合(排序后)
// 比较才不会把同一组域名的续期证书误判为新条目而留下重复。
func hostSetKey(hosts []string) string {
	c := append([]string(nil), hosts...)
	sort.Strings(c)
	return strings.Join(c, "\n")
}

// delete 按证书指纹删除对应记录。
func (cs *serverCertStore) delete(id string) {
	id = strings.TrimSpace(id)
	if id == "" {
		return
	}
	cs.mu.Lock()
	defer cs.mu.Unlock()
	for i := range cs.items {
		if _, leaf, ok := parseEntry(cs.items[i]); ok && certFingerprint(leaf) == id {
			cs.items = append(cs.items[:i], cs.items[i+1:]...)
			_ = cs.save() // best-effort:删除若未落盘,最多重启后重现,非密钥丢失
			return
		}
	}
}

// dtos 返回全部导入证书的摘要(不含私钥),供 UI 展示;跳过解析失败的条目(与 certList 一致),
// 避免呈现出 Hosts 为 null、ID 为空、无法删除的僵尸行。
func (cs *serverCertStore) dtos() []ServerCertDTO {
	cs.mu.RLock()
	defer cs.mu.RUnlock()
	out := make([]ServerCertDTO, 0, len(cs.items))
	for _, it := range cs.items {
		if _, leaf, ok := parseEntry(it); ok {
			out = append(out, dtoFromLeaf(leaf))
		}
	}
	return out
}

// certList 返回全部导入证书(每张的 Leaf 已解析好),供下发到引擎;跳过解析失败的条目。
// 主机匹配在引擎侧按各证书自身的 SAN 进行,故这里无需再按 host 展开。
func (cs *serverCertStore) certList() []*tls.Certificate {
	cs.mu.RLock()
	defer cs.mu.RUnlock()
	out := make([]*tls.Certificate, 0, len(cs.items))
	for _, it := range cs.items {
		pair, leaf, ok := parseEntry(it)
		if !ok {
			continue
		}
		c := pair
		c.Leaf = leaf
		out = append(out, &c)
	}
	return out
}

// parseEntry 把一条记录解析为 TLS 证书对与叶子证书;任一步失败返回 ok=false。
func parseEntry(entry ServerCert) (tls.Certificate, *x509.Certificate, bool) {
	pair, err := tls.X509KeyPair([]byte(entry.CertPEM), []byte(entry.KeyPEM))
	if err != nil || len(pair.Certificate) == 0 {
		return tls.Certificate{}, nil, false
	}
	leaf, err := x509.ParseCertificate(pair.Certificate[0])
	if err != nil {
		return tls.Certificate{}, nil, false
	}
	return pair, leaf, true
}

// certHosts 从证书提取用于展示/去重的覆盖域名:DNS SAN + IP SAN;二者皆空时回退 CN。
// 全部小写去重;通配符 SAN(如 *.example.com)原样保留。仅用于 DTO 与续期去重,不用于匹配——
// 匹配在引擎侧按 x509 语义进行(见 servercert.go 的 importedServerCertFor)。
func certHosts(leaf *x509.Certificate) []string {
	seen := make(map[string]struct{})
	var hosts []string
	add := func(h string) {
		h = strings.ToLower(strings.TrimSpace(h))
		if h == "" {
			return
		}
		if _, dup := seen[h]; dup {
			return
		}
		seen[h] = struct{}{}
		hosts = append(hosts, h)
	}
	for _, d := range leaf.DNSNames {
		add(d)
	}
	for _, ip := range leaf.IPAddresses {
		add(ip.String())
	}
	if len(hosts) == 0 {
		add(leaf.Subject.CommonName)
	}
	return hosts
}

// certFingerprint 以叶子证书 DER 的 SHA-256 作为稳定标识(删除引用)。
func certFingerprint(leaf *x509.Certificate) string {
	sum := sha256.Sum256(leaf.Raw)
	return hex.EncodeToString(sum[:])
}

// dtoFromLeaf 从已解析的叶子证书构建公开摘要(不含私钥)。
func dtoFromLeaf(leaf *x509.Certificate) ServerCertDTO {
	return ServerCertDTO{
		ID:       certFingerprint(leaf),
		Hosts:    certHosts(leaf),
		Subject:  leaf.Subject.CommonName,
		Issuer:   leaf.Issuer.CommonName,
		NotAfter: leaf.NotAfter.Format(time.RFC3339),
	}
}
