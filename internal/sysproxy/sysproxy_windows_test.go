// Copyright 2026 The mintfog Authors
// SPDX-License-Identifier: Apache-2.0
// Use of this source code is governed by an Apache 2.0
// license that can be found in the LICENSE file.

//go:build windows

package sysproxy

import (
	"crypto/rand"
	"errors"
	"fmt"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"golang.org/x/sys/windows/registry"
)

type registryCall struct {
	op, name string
	value    any
}

type fakeProxyKey struct {
	calls   *[]registryCall
	failAt  string
	err     error
	enabled uint64
	server  string
}

func (k *fakeProxyKey) record(op, name string, value any) error {
	*k.calls = append(*k.calls, registryCall{op, name, value})
	if op+":"+name == k.failAt {
		return k.err
	}
	return nil
}

func (k *fakeProxyKey) SetStringValue(name, value string) error {
	return k.record("string", name, value)
}

func (k *fakeProxyKey) SetDWordValue(name string, value uint32) error {
	return k.record("dword", name, value)
}

func (k *fakeProxyKey) GetIntegerValue(name string) (uint64, uint32, error) {
	return k.enabled, registry.DWORD, k.record("integer", name, nil)
}

func (k *fakeProxyKey) GetStringValue(name string) (string, uint32, error) {
	return k.server, registry.SZ, k.record("get", name, nil)
}

func (k *fakeProxyKey) Close() error {
	return k.record("close", "", nil)
}

func fakeWindowsProxy(key *fakeProxyKey, openErr error) windowsProxy {
	return windowsProxy{
		open: func(access uint32) (proxyKey, error) {
			*key.calls = append(*key.calls, registryCall{"open", "", access})
			if openErr != nil {
				return nil, openErr
			}
			return key, nil
		},
		refresh: func() { *key.calls = append(*key.calls, registryCall{"refresh", "", nil}) },
	}
}

func TestWindowsProxyWrites(t *testing.T) {
	t.Parallel()
	failure := errors.New("注册表访问失败")
	for _, tt := range []struct {
		name   string
		clear  bool
		writes []registryCall
	}{
		{"设置", false, []registryCall{
			{"string", "ProxyServer", "proxy.example:3128"},
			{"string", "ProxyOverride", "localhost;127.0.0.1;<local>"},
			{"dword", "ProxyEnable", uint32(1)},
		}},
		{"清除", true, []registryCall{{"dword", "ProxyEnable", uint32(0)}}},
	} {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			for fail := -2; fail < len(tt.writes); fail++ {
				name := "全部成功"
				if fail == -1 {
					name = "打开失败"
				} else if fail >= 0 {
					name = tt.writes[fail].name + "失败"
				}
				t.Run(name, func(t *testing.T) {
					t.Parallel()
					var calls []registryCall
					key := &fakeProxyKey{calls: &calls, err: failure}
					var openErr error
					if fail == -1 {
						openErr = failure
					}
					if fail >= 0 {
						key.failAt = tt.writes[fail].op + ":" + tt.writes[fail].name
					}
					proxy := fakeWindowsProxy(key, openErr)

					var err error
					if tt.clear {
						err = proxy.clear()
					} else {
						err = proxy.set("proxy.example", 3128)
					}

					want := []registryCall{{"open", "", uint32(registry.SET_VALUE)}}
					switch {
					case fail == -2:
						require.NoError(t, err)
						want = append(want, tt.writes...)
						want = append(want, registryCall{"refresh", "", nil}, registryCall{"close", "", nil})
					case fail == -1:
						require.ErrorIs(t, err, failure)
						assert.Contains(t, err.Error(), "打开注册表失败")
					default:
						require.ErrorIs(t, err, failure)
						want = append(want, tt.writes[:fail+1]...)
						want = append(want, registryCall{"close", "", nil})
					}
					assert.Equal(t, want, calls)
				})
			}
		})
	}
}

func TestWindowsProxyPointsTo(t *testing.T) {
	t.Parallel()

	for _, tt := range []struct {
		name      string
		enabled   uint64
		server    string
		failAt    string
		openError bool
		reads     int
		want      bool
	}{
		{"匹配", 1, "localhost:8080", "", false, 2, true},
		{"已停用", 0, "localhost:8080", "", false, 1, false},
		{"异常启用值", 2, "localhost:8080", "", false, 1, false},
		{"主机不匹配", 1, "other:8080", "", false, 2, false},
		{"端口不匹配", 1, "localhost:8081", "", false, 2, false},
		{"空地址", 1, "", "", false, 2, false},
		{"逐协议配置不精确匹配", 1, "http=localhost:8080;https=localhost:8080", "", false, 2, false},
		{"打开失败", 1, "localhost:8080", "", true, 0, false},
		{"读取启用状态失败", 1, "localhost:8080", "integer:ProxyEnable", false, 1, false},
		{"读取地址失败", 1, "localhost:8080", "get:ProxyServer", false, 2, false},
	} {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			var calls []registryCall
			failure := errors.New("读取失败")
			key := &fakeProxyKey{
				calls:   &calls,
				failAt:  tt.failAt,
				err:     failure,
				enabled: tt.enabled,
				server:  tt.server,
			}
			var openErr error
			if tt.openError {
				openErr = failure
			}
			proxy := fakeWindowsProxy(key, openErr)
			assert.Equal(t, tt.want, proxy.pointsTo("localhost", 8080))
			want := []registryCall{{"open", "", uint32(registry.QUERY_VALUE)}}
			if tt.reads > 0 {
				want = append(want, registryCall{"integer", "ProxyEnable", nil})
			}
			if tt.reads > 1 {
				want = append(want, registryCall{"get", "ProxyServer", nil})
			}
			if !tt.openError {
				want = append(want, registryCall{"close", "", nil})
			}
			assert.Equal(t, want, calls)
		})
	}
}

func TestWindowsProxyRegistryLifecycle(t *testing.T) {
	t.Parallel()

	// 临时注册表项与刷新替身共同隔离测试，避免影响当前用户的代理及桌面应用。
	path := `Software\SniffySysproxyTest-` + rand.Text()
	key, _, err := registry.CreateKey(registry.CURRENT_USER, path, registry.ALL_ACCESS)
	require.NoError(t, err)
	t.Cleanup(func() {
		assert.NoError(t, key.Close())
		assert.NoError(t, registry.DeleteKey(registry.CURRENT_USER, path))
	})
	refreshes := 0
	proxy := windowsProxy{
		open: func(access uint32) (proxyKey, error) {
			return registry.OpenKey(registry.CURRENT_USER, path, access)
		},
		refresh: func() { refreshes++ },
	}
	assert.False(t, proxy.pointsTo("127.0.0.1", 8080))
	for _, address := range []struct {
		host string
		port int
	}{
		{"127.0.0.1", 8080},
		{"proxy.example", 65535},
	} {
		require.NoError(t, proxy.set(address.host, address.port))
		assert.True(t, proxy.pointsTo(address.host, address.port))
		assert.False(t, proxy.pointsTo(address.host, address.port-1))
		server, kind, err := key.GetStringValue("ProxyServer")
		require.NoError(t, err)
		assert.Equal(t, uint32(registry.SZ), kind)
		assert.Equal(t, fmt.Sprintf("%s:%d", address.host, address.port), server)
		bypass, _, err := key.GetStringValue("ProxyOverride")
		require.NoError(t, err)
		assert.Equal(t, "localhost;127.0.0.1;<local>", bypass)
		require.NoError(t, proxy.clear())
		assert.False(t, proxy.pointsTo(address.host, address.port))
		enabled, kind, err := key.GetIntegerValue("ProxyEnable")
		require.NoError(t, err)
		assert.Equal(t, uint32(registry.DWORD), kind)
		assert.Zero(t, enabled)
		retained, _, err := key.GetStringValue("ProxyServer")
		require.NoError(t, err)
		assert.Equal(t, server, retained)
	}
	require.NoError(t, proxy.clear())
	assert.Equal(t, 5, refreshes)
	require.NoError(t, key.SetStringValue("ProxyEnable", "1"))
	assert.False(t, proxy.pointsTo("proxy.example", 65535))
	require.NoError(t, key.SetDWordValue("ProxyEnable", 1))
	require.NoError(t, key.SetDWordValue("ProxyServer", 8080))
	assert.False(t, proxy.pointsTo("proxy.example", 65535))
}
