// Copyright 2026 The mintfog Authors
// SPDX-License-Identifier: Apache-2.0
// Use of this source code is governed by an Apache 2.0
// license that can be found in the LICENSE file.

package netinfo

import (
	"strings"
	"testing"
)

func TestParseHardwarePorts(t *testing.T) {
	cases := []struct {
		name string
		in   string
		want map[string]string
	}{
		{
			name: "normal two entries",
			in: "Hardware Port: Wi-Fi\nDevice: en0\nEthernet Address: aa:bb:cc:dd:ee:ff\n\n" +
				"Hardware Port: Ethernet\nDevice: en1\nEthernet Address: 11:22:33:44:55:66\n",
			want: map[string]string{"en0": "Wi-Fi", "en1": "Ethernet"},
		},
		{
			name: "device without port is dropped",
			in:   "Device: en0\nEthernet Address: aa:bb:cc:dd:ee:ff\n",
			want: map[string]string{},
		},
		{
			name: "port without device is dropped",
			in:   "Hardware Port: Wi-Fi\nEthernet Address: aa:bb:cc:dd:ee:ff\n",
			want: map[string]string{},
		},
		{
			// 现实里 networksetup 每块以 Device 结束,port 读到 Device 时应清零,
			// 后续只有 Device 的残缺块不能继承上一条的 port。
			name: "port cleared after device, no leak to next block",
			in: "Hardware Port: Wi-Fi\nDevice: en0\n\n" +
				"Device: en1\n",
			want: map[string]string{"en0": "Wi-Fi"},
		},
		{
			name: "CRLF line endings",
			in:   "Hardware Port: Wi-Fi\r\nDevice: en0\r\n",
			want: map[string]string{"en0": "Wi-Fi"},
		},
		{
			name: "empty device value dropped",
			in:   "Hardware Port: Wi-Fi\nDevice: \n",
			want: map[string]string{},
		},
		{
			name: "empty input",
			in:   "",
			want: map[string]string{},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := parseHardwarePorts(strings.NewReader(tc.in))
			if len(got) != len(tc.want) {
				t.Fatalf("parseHardwarePorts len = %d (%+v), want %d (%+v)", len(got), got, len(tc.want), tc.want)
			}
			for k, v := range tc.want {
				if got[k] != v {
					t.Errorf("parseHardwarePorts[%q] = %q, want %q", k, got[k], v)
				}
			}
		})
	}
}
