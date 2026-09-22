#!/usr/bin/env bash
# Copyright 2026 The mintfog Authors
# SPDX-License-Identifier: Apache-2.0

set -euo pipefail
release_root="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
cd "$release_root"
exec go run ./cmd/sniffy-release manifest "$@"
