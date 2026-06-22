// Copyright 2026 The mintfog Authors
// SPDX-License-Identifier: Apache-2.0
// Use of this source code is governed by an Apache 2.0
// license that can be found in the LICENSE file.

package sysproxy

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestParseNetworkServices(t *testing.T) {
	raw := "An asterisk (*) denotes that a network service is disabled.\n" +
		"Wi-Fi\n" +
		"Ethernet\n" +
		"*Bluetooth PAN\n" +
		"Thunderbolt Bridge\n"
	got := parseNetworkServices(raw)
	assert.Equal(t, []string{"Wi-Fi", "Ethernet", "Thunderbolt Bridge"}, got)
}

func TestParseNetworkServicesEmpty(t *testing.T) {
	assert.Empty(t, parseNetworkServices(""))
	// 仅有表头时也应为空。
	assert.Empty(t, parseNetworkServices("An asterisk (*) denotes that a network service is disabled.\n"))
}

func TestParseGetWebProxy(t *testing.T) {
	enabled, server, port := parseGetWebProxy("Enabled: Yes\nServer: 127.0.0.1\nPort: 8080\nAuthenticated Proxy Enabled: 0\n")
	assert.True(t, enabled)
	assert.Equal(t, "127.0.0.1", server)
	assert.Equal(t, "8080", port)

	off, _, _ := parseGetWebProxy("Enabled: No\nServer:\nPort: 0\n")
	assert.False(t, off)
}
