# README 演示素材

两份 README 的主图和演示动图托管在 R2，通过 `https://cdn.gosniffy.com/readme/` 下的版本前缀引用。只有工作台界面或者演示流程有明显变化时才重新生成。

## 重新生成

`generate` 只支持 macOS 和 Linux。需要准备 Go 1.26+、Node.js 22+、OpenSSL 和 FFmpeg。最好在 macOS 上重新录制，以复现发布素材中的字体回退和标题栏。

在仓库根目录运行：

```sh
npm ci --prefix web
npm ci --prefix scripts/readme-media
npm --prefix scripts/readme-media exec -- playwright install chromium
npm --prefix scripts/readme-media run generate
```

生成脚本会构建前端、启动本地演示服务和 Wails 浏览器工作台，然后录制深色的简体中文和英文素材各一套。Sniffy 代理固定监听 `127.0.0.1:8080`，端口被占的时候生成脚本会报错退出。生成的输出位于 `scripts/readme-media/out/`，目录与 R2 桶根目录对应。

可以用 `FFMPEG=/path/to/ffmpeg` 指定 FFmpeg。录制默认用 Playwright 自带的 Chromium，可以用 `README_BROWSER_CHANNEL=chrome` 改用本机安装的 Chrome。如果 12 fps 紧凑镜头依旧超过了 5 MB，会用 gifsicle 轻度压缩，gifsicle 的路径可以通过 `GIFSICLE` 指定。出现录制问题时设置 `README_KEEP_TEMP=1`，会保留 WebM 原始文件和采集帧，以及采集失败时的网页截图。

## 发布素材

1. 每批素材用一个新的版本前缀，重新生成前将 `generate.mjs` 内 `mediaPrefix` 改成下一个版本，例如 `readme/v2`。CDN 边缘和 GitHub 图片代理都会缓存同名对象，在覆盖掉已有的版本之后 README 有可能还是显示老图。
2. 将 `out/` 内容按照原有目录结构上传到 R2 桶根目录，对象的 Content-Type 必须要是 `image/png` 或者 `image/gif`，GitHub 才会显示图片；R2 控制台和 rclone 会根据扩展名设置 Content-Type。
3. 确认新地址可以访问后，将 `README.md` 和 `README.en.md` 的图片地址改到新前缀。

## 单独启动演示服务

在仓库根目录分别打开三个终端。在第一个终端中启动测试源站和域名转发：

```sh
npm --prefix scripts/readme-media run serve
```

再在第二个终端中启动 Sniffy：

```sh
npm --prefix web run build
demo_profile="$(mktemp -d)/profile"
echo "$demo_profile"
go run -buildvcs=false -tags desktop,server ./scripts/readme-media/workbench -state "$demo_profile"
```

在浏览器中打开 `http://127.0.0.1:18180/?theme=dark`。在第三个终端使用上一步输出的临时配置路径发起请求：

```sh
npm --prefix scripts/readme-media run traffic -- /path/to/profile/sniffy-ca.pem http://127.0.0.1:8080 seed zh-CN
```

`seed` 会发起 26 条会话，包含 HTTPS JSON、图片、WebSocket、gRPC 和 SSE。也可以用 `chat`、`quote` 或者 `stock` 单独触发对应的演示。`*.shop.test` 会被本地转发映射到自签名 HTTPS 源站，客户端会通过演示实例的 Sniffy 根证书验证代理签发的证书。结束后在前两个终端都按下 Ctrl+C 停止进程。
