# 发布工具

`cmd/sniffy-release` 负责生成发布清单、上传 R2 版本目录和更新官网下载入口。制品读取为字节数组，校验通过后使用 AWS SDK for Go 的 S3 客户端上传，并与回读内容逐字节比较。官网与客户端通过 `release.json` 共享数据契约。

```bash
go build -o /tmp/sniffy-release ./cmd/sniffy-release
/tmp/sniffy-release manifest --version v1.2.3 --dir release \
  --out release/release.json --checksums release/SHA256SUMS --require-complete \
  --base https://cdn.gosniffy.com/releases \
  --notes https://github.com/mintfog/sniffy/releases/tag/v1.2.3 \
  --published 2026-09-22
/tmp/sniffy-release stage --dir release
# GitHub Release 的同版本附件发布完成后，更新官网下载入口。
/tmp/sniffy-release promote --dir release
```

`stage` 与 `promote` 读取 `R2_ACCOUNT_ID`、`R2_BUCKET`、`AWS_ACCESS_KEY_ID`、`AWS_SECRET_ACCESS_KEY`。工作流从仓库变量和 Secrets 注入这些值。网络请求支持取消和有限重试，操作总超时默认 30 分钟，可用 `--timeout` 调整。

`stage` 在全部本地制品与清单一致后开始上传，并验证 R2 回读内容及公开下载地址。版本目录的 `release.json` 最后写入，表示该版本已完成校验。同版本文件内容发生变化时拒绝覆盖；重跑可复用内容一致的文件。清单按字段内容比较，JSON 的键顺序、空白及等价转义不影响重发。

`promote` 要求远端版本清单与输入一致。官网下载入口只前进到更新版本；首个正式版之前可以跟随预发布，正式版发布后只更新正式版。自动发布与手动同步工作流共用 `r2-publish` 并发组。对同一桶的本地操作也必须串行执行，检查与写入不提供跨入口的原子锁。

`version <标签>` 输出规范化版本和 `prerelease` 状态，供工作流写入步骤输出。`scripts/release-manifest.sh` 调用 Go 工具的 `manifest` 操作，参数保持一致，运行需要仓库 `go.mod` 指定的 Go 版本。

本地测试使用内存存储及 HTTP/TLS 测试服务，不需要 R2 凭据：

```bash
go test -race ./internal/release ./cmd/sniffy-release
bash scripts/tests/release-manifest.test.sh
```
