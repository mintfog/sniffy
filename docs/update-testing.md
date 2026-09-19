# 应用内更新测试

更新功能的测试覆盖清单解析、版本选择、下载校验、取消与重试、状态通知、配置持久化，以及用户从检查到安装的界面操作。网络请求使用本地 HTTP/TLS 服务，定时调度使用 Go `testing/synctest` 和 Vitest 假时钟，测试可在离线环境运行。

## 本地运行

```bash
# 完整 Go 竞态检查
go test -race -count=1 ./...

# 更新功能语句覆盖率，与 CI 使用同一命令
go test -count=1 \
  -coverpkg=./internal/update,./internal/service,./internal/api,./internal/app,./internal/platform \
  -covermode=atomic -coverprofile=coverage.out \
  ./internal/update ./internal/service ./internal/api ./internal/app ./internal/platform
python3 scripts/check-update-coverage.py coverage.out
go tool cover -html=coverage.out

# 前端纯函数测试、组件交互与覆盖率门槛
npm --prefix web ci
npm --prefix web run test:coverage

# 发布清单与覆盖率检查器
bash scripts/tests/release-manifest.test.sh
python3 scripts/tests/check-update-coverage.test.py

# 安装了桌面构建依赖的环境
go test -race -count=1 -tags desktop ./internal/desktop ./internal/update ./internal/service
```

前端 HTML 报告位于 `web/coverage/index.html`。CI 保存各系统的 Go profile 及前端 HTML、JSON、LCOV 报告。

## 覆盖率范围与门槛

| 范围 | 指标 | 最低值 |
| --- | --- | --- |
| `internal/update/` | 包内语句覆盖率 | 95% |
| `internal/service/update.go` | 文件语句覆盖率 | 95% |
| `internal/api/update.go` | 文件语句覆盖率 | 95% |
| `internal/app/update.go` | 文件语句覆盖率 | 95% |
| `internal/platform/paths.go` | 文件语句覆盖率 | 90% |
| 前端 `update.ts`、`updateSync.ts`、`links.ts`、`AboutView.tsx`、`StatusBar.tsx` | 每个文件的语句、行、函数、分支覆盖率 | 均为 95% |

门槛针对上述固定功能范围。设置页自动检查开关另有交互测试，真实 `Bridge` 方法经模拟 Wails 运行时执行。整个设置页、整个 `bridge.ts`、工作台入口、官网页面及原生安装器不计入前端门槛；按 Git 改动行统计时，设置页和 Bridge 的修改行另行计入。Go 与前端覆盖率的统计单位不同，分别报告。

Go 覆盖率检查会合并跨测试包重复的代码块；任一范围缺失、报告格式错误或覆盖率不足都会令 CI 失败。完整竞态检查和覆盖率采集各自运行，覆盖率插桩集中在更新功能所在包。

## 关键回归场景

- 主清单源不可用时切换备用源；请求取消后结束检查。
- 下载断流、大小或 SHA256 不符时清理临时文件；保留此前有效安装包。
- 下载取消、失败重试、退出清理；启动下载的 HTTP 请求结束后后台任务继续。
- 区分检查与下载失败，下载失败后可直接重试；免安装程序提供文件夹入口，安装包按平台提供安装动作。
- 下载完成后检查失败仍保留安装入口；安装包被移动或删除后恢复下载入口，重试清除旧错误。
- 迟到快照不覆盖新状态；丢失事件后定时回查；窗口卸载释放订阅与定时器。
- 自动检查与跳过版本持久化、多窗口同步；后台启动延迟、周期检查和退出取消。
- 发布清单的实际文件大小、校验值、平台信息、安装包优先级及重复生成结果。

## 原生安装验证

桌面 Bridge 测试在 Windows/macOS CI 中执行，覆盖检查、下载和文件缺失恢复。系统安装器启动涉及真实桌面环境，发布前应在对应系统验证：Windows 授权取消后应用保持可用，授权成功后退出并完成替换与重启；macOS 打开下载的镜像；Linux 定位到下载文件。该部分不算作自动化测试已覆盖。
