// Copyright 2026 The mintfog Authors
// SPDX-License-Identifier: Apache-2.0
// Use of this source code is governed by an Apache 2.0
// license that can be found in the LICENSE file.

package ca

import (
	"crypto/ecdsa"
	"crypto/rsa"
	"crypto/x509"
	"encoding/pem"
	"errors"
	"fmt"
	"strings"

	pkcs12 "software.sslmate.com/src/go-pkcs12"
)

// 支持的导出格式。crt/pem 内容相同(都是 PEM 编码的证书),仅扩展名不同,
// 便于用户按目标系统习惯选择;der/p12 为二进制。
const (
	FormatPEM    = "pem"
	FormatCRT    = "crt"
	FormatDER    = "der"
	FormatP12    = "p12"
	FormatBundle = "bundle"
)

// ExportRootCertPEM 返回根证书的 PEM 编码(单一 CERTIFICATE 块)。
func ExportRootCertPEM(c CA) []byte {
	if c == nil || c.GetCA() == nil {
		return nil
	}
	return pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: c.GetCA().Raw})
}

// ExportRootCertDER 返回根证书的裸 DER 字节。
func ExportRootCertDER(c CA) []byte {
	if c == nil || c.GetCA() == nil {
		return nil
	}
	return c.GetCA().Raw
}

// ExportRootKeyPEM 返回根私钥的 PEM 编码。ECDSA 用 EC PRIVATE KEY,其它统一走 PKCS8。
func ExportRootKeyPEM(c CA) ([]byte, error) {
	if c == nil || c.GetCAKey() == nil {
		return nil, errors.New("root CA private key unavailable")
	}
	return encodePrivateKeyPEM(c.GetCAKey())
}

// ExportRootBundlePEM 返回证书 + 私钥的联合 PEM,单文件可直接给部分工具使用。
func ExportRootBundlePEM(c CA) ([]byte, error) {
	certPEM := ExportRootCertPEM(c)
	if certPEM == nil {
		return nil, errors.New("root CA certificate unavailable")
	}
	keyPEM, err := ExportRootKeyPEM(c)
	if err != nil {
		return nil, err
	}
	return append(certPEM, keyPEM...), nil
}

// ExportRootPKCS12 用给定口令把根证书与私钥打包成 PKCS#12(.p12/.pfx)。
// 使用 Modern2023 profile(AES-256/SHA-256),各主流系统均可导入;
// 但 macOS 系统钥匙串 legacy 版工具不支持时可换用 pkcs12.LegacyRC2。
func ExportRootPKCS12(c CA, password string) ([]byte, error) {
	if c == nil || c.GetCA() == nil {
		return nil, errors.New("root CA certificate unavailable")
	}
	key := c.GetCAKey()
	if key == nil {
		return nil, errors.New("root CA private key unavailable")
	}
	return pkcs12.Modern2023.Encode(key, c.GetCA(), nil, password)
}

// ImportFromPKCS12 从 .p12/.pfx 字节流中解出根证书与私钥。
// pkcs12 只捆绑叶证书对应的私钥,caChain 里的 CA 没有配套私钥,无法用来签发,故拒绝。
func ImportFromPKCS12(data []byte, password string) (*x509.Certificate, any, error) {
	if len(data) == 0 {
		return nil, nil, errors.New("PKCS12 数据为空")
	}
	key, cert, _, err := pkcs12.DecodeChain(data, password)
	if err != nil {
		return nil, nil, fmt.Errorf("解析 PKCS12 失败(口令是否正确?): %w", err)
	}
	if cert == nil {
		return nil, nil, errors.New("PKCS12 不含叶子证书")
	}
	if !cert.IsCA {
		return nil, nil, fmt.Errorf(
			"PKCS12 首个证书 CN=%q 不是 CA;请重新导出仅包含根 CA(及其私钥)的 PKCS12",
			cert.Subject.CommonName)
	}
	if key == nil {
		return nil, nil, errors.New("PKCS12 不含私钥")
	}
	if err := ensureSignerMatchesCert(key, cert); err != nil {
		return nil, nil, err
	}
	return cert, key, nil
}

// ImportFromPEMBundle 从"证书+私钥"的联合 PEM 中解析根 CA。
// 允许两块顺序任意;若 PEM 里有多张证书,取第一张 IsCA 的。
func ImportFromPEMBundle(data []byte) (*x509.Certificate, any, error) {
	if len(data) == 0 {
		return nil, nil, errors.New("PEM 数据为空")
	}
	var (
		root *x509.Certificate
		key  any
	)
	rest := data
	sawBlock := false
	for {
		block, remaining := pem.Decode(rest)
		if block == nil {
			break
		}
		sawBlock = true
		rest = remaining
		typ := strings.ToUpper(block.Type)
		switch {
		case typ == "CERTIFICATE":
			c, err := x509.ParseCertificate(block.Bytes)
			if err != nil {
				return nil, nil, fmt.Errorf("解析证书 PEM 失败: %w", err)
			}
			if root == nil && c.IsCA {
				root = c
			}
		case strings.Contains(typ, "PRIVATE KEY"):
			if strings.Contains(typ, "ENCRYPTED") || block.Headers["DEK-Info"] != "" {
				return nil, nil, errors.New("PEM 中的私钥被口令加密,当前不支持;请先解密(openssl rsa/pkey -in <in> -out <out>)后重试")
			}
			if key != nil {
				continue
			}
			k, err := parsePrivateKeyDER(block.Type, block.Bytes)
			if err != nil {
				return nil, nil, fmt.Errorf("解析私钥 PEM 失败: %w", err)
			}
			key = k
		}
	}
	if !sawBlock {
		return nil, nil, errors.New("未识别到任何 PEM 块(文件是否有效的 PEM?)")
	}
	if root == nil {
		return nil, nil, errors.New("未找到 CA 证书(需要一张 BasicConstraints CA=true 的根)")
	}
	if key == nil {
		return nil, nil, errors.New("未找到匹配的私钥")
	}
	if err := ensureSignerMatchesCert(key, root); err != nil {
		return nil, nil, err
	}
	return root, key, nil
}

// encodePrivateKeyPEM 把私钥编成 PEM。ECDSA 走 SEC1 与 loadCA 的 EC 分支保持一致,其它走 PKCS8。
func encodePrivateKeyPEM(key any) ([]byte, error) {
	if k, ok := key.(*ecdsa.PrivateKey); ok {
		der, err := x509.MarshalECPrivateKey(k)
		if err != nil {
			return nil, err
		}
		return pem.EncodeToMemory(&pem.Block{Type: "EC PRIVATE KEY", Bytes: der}), nil
	}
	der, err := x509.MarshalPKCS8PrivateKey(key)
	if err != nil {
		return nil, fmt.Errorf("不支持的私钥类型 %T: %w", key, err)
	}
	return pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: der}), nil
}

// parsePrivateKeyDER 按 PEM Type 与探测顺序解出私钥,尽量兼容常见的外部导入。
func parsePrivateKeyDER(pemType string, der []byte) (any, error) {
	typ := strings.ToUpper(pemType)
	switch {
	case strings.Contains(typ, "EC PRIVATE KEY"):
		if k, err := x509.ParseECPrivateKey(der); err == nil {
			return k, nil
		}
	case strings.Contains(typ, "RSA PRIVATE KEY"):
		if k, err := x509.ParsePKCS1PrivateKey(der); err == nil {
			return k, nil
		}
	}
	if k, err := x509.ParsePKCS8PrivateKey(der); err == nil {
		return k, nil
	}
	if k, err := x509.ParseECPrivateKey(der); err == nil {
		return k, nil
	}
	if k, err := x509.ParsePKCS1PrivateKey(der); err == nil {
		return k, nil
	}
	return nil, errors.New("无法解析私钥(尝试了 PKCS8/EC/PKCS1)")
}

// ensureSignerMatchesCert 快速校验私钥与证书公钥属同一对,防止误传导致后续签叶子证书失败。
func ensureSignerMatchesCert(key any, cert *x509.Certificate) error {
	switch pub := cert.PublicKey.(type) {
	case *ecdsa.PublicKey:
		k, ok := key.(*ecdsa.PrivateKey)
		if !ok {
			return fmt.Errorf("证书公钥为 ECDSA,私钥类型不匹配(%T)", key)
		}
		if !k.PublicKey.Equal(pub) {
			return errors.New("证书公钥与私钥不匹配")
		}
	case *rsa.PublicKey:
		k, ok := key.(*rsa.PrivateKey)
		if !ok {
			return fmt.Errorf("证书公钥为 RSA,私钥类型不匹配(%T)", key)
		}
		if !k.PublicKey.Equal(pub) {
			return errors.New("证书公钥与私钥不匹配")
		}
	default:
		return fmt.Errorf("暂不支持的证书公钥类型 %T", cert.PublicKey)
	}
	return nil
}
