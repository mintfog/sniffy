---
title: Installation
description: Download a desktop installer or a headless binary, or build Sniffy from source.
---

## Which edition do you need

| Edition          | Good for                                              | How you interact  |
| ---------------- | ----------------------------------------------------- | ----------------- |
| Desktop app      | Everyday capture, editing and debugging with a UI     | A native window   |
| Headless service | Servers, containers, CI, and scripted debugging       | REST + WebSocket  |

Both share the same capture engine, rule engine, and plugin system, and they read the same configuration directory — you can switch between them on one machine.

## Desktop app

Pick an installer for your platform on [GitHub Releases](https://github.com/mintfog/sniffy/releases).

| OS      | Architecture   | Installer                                    |
| ------- | -------------- | -------------------------------------------- |
| Windows | x64            | `sniffy-desktop-windows-amd64-installer.exe` |
| macOS   | Apple silicon  | `sniffy-desktop-darwin-arm64-installer.dmg`  |
| macOS   | Intel          | `sniffy-desktop-darwin-amd64-installer.dmg`  |
| Linux   | x64            | `sniffy-desktop-linux-amd64-installer.deb`   |

### Windows

Run the `.exe` and follow the wizard. If SmartScreen shows the "Windows protected your PC" prompt, choose "More info - Run anyway".

The Windows desktop app requires Microsoft Edge WebView2 Runtime. If it is missing or the app opens a blank window, install [WebView2 Runtime](https://developer.microsoft.com/microsoft-edge/webview2/).

### macOS

Open the `.dmg` and drag Sniffy into Applications. The build is ad-hoc signed and does not yet have an Apple developer certificate or notarization, so Gatekeeper blocks the first launch:

- Right-click Sniffy in Applications, choose "Open", and confirm in the dialog; or
- Go to System Settings - Privacy & Security and click "Open Anyway" for the blocked app.

If macOS reports that the app is damaged, first verify that the download is complete and came from this project’s Releases page. If the quarantine attribute is causing the block, run:

```bash
xattr -dr com.apple.quarantine /Applications/Sniffy.app
```

### Linux

The `.deb` targets Debian, Ubuntu, and derivatives:

```bash
sudo apt install ./sniffy-desktop-linux-amd64-installer.deb
```

The package installs `/usr/bin/sniffy` plus a desktop entry and icons. Dependencies are resolved at build time with `dpkg-shlibdeps`, mostly the system WebKitGTK components. Launch it from your application menu, or just run `sniffy`.

For other distributions, build from source below, or run the headless service in a container.

## Headless service

Headless binaries on Releases are named `sniffy-<os>-<arch>`, covering `linux/amd64`, `linux/arm64`, `darwin/amd64`, `darwin/arm64`, `windows/amd64`, and `windows/arm64`. Each is a single static file with no runtime dependencies.

```bash
chmod +x sniffy-linux-amd64
./sniffy-linux-amd64 -version
./sniffy-linux-amd64
```

By default the proxy listens on `0.0.0.0:8080` and the management API on `127.0.0.1:8888`. See [Headless service](/en/docs/headless/).

## Build from source

You need Go 1.26+. The frontend and the desktop build also need Node.js 22.12+.

```bash
git clone https://github.com/mintfog/sniffy.git
cd sniffy
```

### Headless

```bash
go build -o sniffy ./cmd/sniffy
```

Cross-compile into `dist/`:

```bash
bash scripts/build.sh headless linux/amd64 darwin/arm64 windows/amd64
```

Headless does not depend on CGO, so one machine can cross-compile for every target.

### Desktop

The desktop build uses Wails v3, needs the `desktop` build tag, and requires the frontend to be built first:

```bash
cd web && npm ci && npm run build && cd ..
go run -tags desktop .
```

Production build:

```bash
bash scripts/build.sh desktop
```

The build script is bash; on Windows, run it under Git Bash.

Platform requirements:

- **Windows** uses WebView2, does not depend on CGO, and supports cross-compilation: `task desktop:windows`, or `task desktop:windows:installer` for an NSIS package.
- **macOS / Linux** need CGO and the system webview development packages (WebKitGTK and its headers on Linux).

Without the `desktop` tag the repository root entry point is a stub, so `go build ./...` passes even on machines with no webview dependencies.

## Where data lives

Both editions read the same configuration and rules.

| Purpose       | Location                                                           |
| ------------- | ------------------------------------------------------------------ |
| Configuration | Linux `~/.config/sniffy`, macOS `~/Library/Application Support/sniffy`, Windows `%AppData%\sniffy` |
| Cache         | Linux `~/.cache/sniffy`, macOS `~/Library/Caches/sniffy`, Windows `%LocalAppData%\sniffy` |

Configuration holds `config.json`, `rules.json`, logs, user plugins, and certificates. The cache only holds on-disk copies of large response bodies and can be deleted at any time. Full details in the [Configuration reference](/en/docs/configuration/).

## Upgrading and uninstalling

To upgrade, install over the existing version. Installers don't touch the configuration directory, so your rules, plugins, and root certificate survive.

Uninstall the usual way for your platform (Windows "Apps & features", delete the app on macOS, `sudo apt remove sniffy` on Linux). Uninstalling does not remove the configuration and cache directories, and it does not remove the root certificate from your system trust store — if you're done with Sniffy, delete those directories yourself and remove Sniffy Root CA from the trust store, see [Removing the root certificate](/en/docs/https/#removing-the-root-certificate).
