# macOS 安装镜像

DMG 使用 `dmgbuild` 保存 Finder 的窗口、图标位置和背景设置。安装窗口为 720 × 460，应用在左、Applications 在右，图标尺寸为 128。卷名称为 `Sniffy Installer`，卷图标复用应用图标。

本地打包前，在 Python 3.10 及以上版本的虚拟环境中安装固定版本依赖：

```bash
python3 -m venv /tmp/sniffy-dmg-tools
/tmp/sniffy-dmg-tools/bin/python -m pip install -r build/darwin/dmg-requirements.txt
export PATH="/tmp/sniffy-dmg-tools/bin:$PATH"
bash scripts/make-dmg-installer.sh \
  --binary dist/sniffy-desktop-darwin-arm64 \
  --version 2.0.0 --arch arm64 --out dist/Sniffy.dmg
```

CI 自动安装这些依赖。打包仍在 macOS 上执行，由系统工具生成应用图标、签名并验证镜像；Finder 布局直接写入 `.DS_Store`，可在后台完成。

`dmg-background.svg` 是可编辑的背景源文件，PNG 为打包资源。两份 PNG 分别为 720 × 460 和 1440 × 920，`dmgbuild` 将它们合成为支持 Retina 的背景。图标是 Finder 中可拖动的真实文件，背景只包含标题、说明和箭头。

使用 ImageMagick 6 的 SVG 渲染器和 DejaVu Sans 字体更新背景：

```bash
convert -background none -density 192 build/darwin/dmg-background.svg \
  -units PixelsPerInch -density 144 PNG24:build/darwin/dmg-background@2x.png
convert build/darwin/dmg-background@2x.png -resize 720x460 \
  -units PixelsPerInch -density 72 PNG24:build/darwin/dmg-background.png
```

修改背景尺寸或箭头位置时，同步调整 `dmg-settings.py` 中的窗口和图标坐标。
