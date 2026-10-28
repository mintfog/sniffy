// Copyright 2026 The mintfog Authors
// SPDX-License-Identifier: Apache-2.0
// Use of this source code is governed by an Apache 2.0
// license that can be found in the LICENSE file.

package notices

import _ "embed"

// Text 包含随程序分发的项目与第三方许可证、版权声明及源码获取说明。
//
//go:embed THIRD_PARTY_NOTICES.txt
var Text string
