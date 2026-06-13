// Copyright 2026 The mintfog Authors
// SPDX-License-Identifier: Apache-2.0
// Use of this source code is governed by an Apache 2.0
// license that can be found in the LICENSE file.

package http

import "testing"

func TestIsCertDomain(t *testing.T) {
	cases := []struct {
		host string
		want bool
	}{
		{"cert.sniffy", true},
		{"cert.sniffy:80", true},
		{"cert.sniffy:443", true},
		{"example.com", false},
		{"example.com:80", false},
		{"", false},
		{"sniffy", false},
		{"cert.sniffy.com", false},
		{"acert.sniffy", false},
	}
	for _, c := range cases {
		if got := isCertDomain(c.host); got != c.want {
			t.Errorf("isCertDomain(%q) = %v, want %v", c.host, got, c.want)
		}
	}
}
