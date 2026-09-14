// Copyright 2026 The mintfog Authors
// SPDX-License-Identifier: Apache-2.0
// Use of this source code is governed by an Apache 2.0
// license that can be found in the LICENSE file.

//go:build desktop && !darwin

package desktop

import "testing"

func TestUILangUsesEnvironmentLocale(t *testing.T) {
	t.Setenv("LC_ALL", "zh-TW.UTF-8")
	if got := uiLang(); got != "zh-Hant" {
		t.Fatalf("uiLang() = %q，期望 zh-Hant", got)
	}
}
