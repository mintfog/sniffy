#!/usr/bin/env python3
# Copyright 2026 The mintfog Authors
# SPDX-License-Identifier: Apache-2.0

"""按更新功能的固定范围检查 Go 语句覆盖率。"""

import argparse
import re
from pathlib import Path

MODULE = "github.com/mintfog/sniffy/"
THRESHOLDS = {
    "internal/update/": 95,
    "internal/service/update.go": 95,
    "internal/api/update.go": 95,
    "internal/app/update.go": 95,
    "internal/platform/paths.go": 90,
}
BLOCK = re.compile(r"(.+):(\d+\.\d+,\d+\.\d+) (\d+) (\d+)")


def read_profile(path):
    lines = Path(path).read_text(encoding="utf-8").splitlines()
    if not lines or lines[0] not in ("mode: set", "mode: count", "mode: atomic"):
        raise ValueError("覆盖率报告缺少有效的 mode 头")
    blocks = {}
    for number, line in enumerate(lines[1:], 2):
        match = BLOCK.fullmatch(line)
        if not match:
            raise ValueError(f"覆盖率报告第 {number} 行格式错误")
        file, span, statements, count = match.groups()
        key = (file.removeprefix(MODULE), span)
        statements, covered = int(statements), int(count) > 0
        if key in blocks:
            previous, hit = blocks[key]
            if previous != statements:
                raise ValueError(f"覆盖率报告同一代码块的语句数不一致：{file}:{span}")
            # -coverpkg 的同一代码块会出现在多个测试包的报告中，只计一次。
            covered = covered or hit
        blocks[key] = (statements, covered)
    return blocks


def check(blocks):
    passed = True
    for scope, minimum in THRESHOLDS.items():
        selected = [
            value for (file, _), value in blocks.items()
            if (file.startswith(scope) if scope.endswith("/") else file == scope)
        ]
        total = sum(n for n, _ in selected)
        covered = sum(n for n, hit in selected if hit)
        if not total:
            print(f"失败：{scope} 缺少覆盖率数据")
            passed = False
            continue
        percent = covered * 100 / total
        ok = covered * 100 >= minimum * total
        print(f"{'通过' if ok else '失败'}：{scope} {covered}/{total} = {percent:.2f}%（门槛 {minimum}%）")
        passed = passed and ok
    return passed


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("profile", help="go test -coverprofile 生成的报告")
    args = parser.parse_args()
    try:
        return 0 if check(read_profile(args.profile)) else 1
    except (OSError, ValueError) as error:
        parser.exit(1, f"覆盖率检查失败：{error}\n")


if __name__ == "__main__":
    raise SystemExit(main())
