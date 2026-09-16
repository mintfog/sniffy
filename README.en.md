<p align="center">
  <img src="web/src/assets/sniffy-mark.png" width="112" alt="Sniffy">
</p>

<h1 align="center">Sniffy</h1>

<p align="center">
  <a href="README.md">中文</a> · <strong>English</strong>
</p>

<p align="center">
  <strong>An open-source HTTP debugging proxy for Windows, macOS, and Linux</strong>
</p>

<p align="center">
  <a href="https://github.com/mintfog/sniffy/releases"><img src="https://img.shields.io/github/v/release/mintfog/sniffy?include_prereleases&amp;style=flat-square&amp;label=release&amp;color=087F8C" alt="Latest release"></a>
  <img src="https://img.shields.io/badge/platform-Windows%20%7C%20macOS%20%7C%20Linux-334155?style=flat-square" alt="Windows / macOS / Linux">
  <a href="LICENSE"><img src="https://img.shields.io/badge/license-Apache%202.0-334155?style=flat-square" alt="Apache License 2.0"></a>
</p>

<p align="center">
  <a href="https://github.com/mintfog/sniffy/releases"><strong>Download Sniffy</strong></a> ·
  <a href="#features">Features</a> ·
  <a href="#getting-started">Getting Started</a> ·
  <a href="#plugins">Plugins</a> ·
  <a href="#documentation">Documentation</a> ·
  <a href="https://github.com/mintfog/sniffy/issues">Report an Issue</a>
</p>

---

Sniffy lets you inspect and modify HTTP and HTTPS traffic from a desktop app. Replay a failing request, mock an API response, or pause traffic at a breakpoint to see what your client is sending and receiving.

For custom behavior, **write JavaScript plugins in the built-in editor**. Add authentication headers, change a response based on its contents, or process WebSocket messages. Save an enabled plugin to apply your changes while you work.

## Installation

**[Download Sniffy from Releases →](https://github.com/mintfog/sniffy/releases)**

Under **Assets** on the release page, download the installer that matches your system:

| Operating system | Processor | Download file |
| --- | --- | --- |
| Windows | x64 | `sniffy-desktop-windows-amd64-installer.exe` |
| macOS | Apple silicon | `sniffy-desktop-darwin-arm64-installer.dmg` |
| macOS | Intel | `sniffy-desktop-darwin-amd64-installer.dmg` |
| Linux | x64 | `sniffy-desktop-linux-amd64-installer.deb` |

<a id="features"></a>

## Features

| Feature | What you can do |
| --- | --- |
| **Inspect traffic** | Read request and response headers and bodies. Search, filter, and mark captured sessions. |
| **Send requests** | Compose a request or edit and resend one you've captured. Import requests from cURL or copy them as cURL commands. |
| **Apply rules** | Rewrite, redirect, block, or mock traffic that matches your rules. |
| **Set breakpoints** | Pause a request or response, make changes, and resume it when you're ready. |
| **Watch messages** | Follow WebSocket conversations and streaming responses, including SSE and gRPC. |
| **Write plugins** | Script custom behavior in JavaScript, choose which URLs it applies to, and view logs as it runs. |
| **Export sessions** | Save HTTP sessions as HAR or export traffic data as JSON. |

<a id="getting-started"></a>

## Getting started

1. **Set the proxy.** Open Sniffy and enable the system proxy, or configure your app to use `127.0.0.1:8080` as its HTTP / HTTPS proxy. If you've changed the port in Settings, use that port instead.
2. **Trust the certificate.** To inspect HTTPS traffic, open Certificate Manager and follow the instructions to install and trust Sniffy's root certificate. For traffic from another device, install and trust the certificate on that device too.
3. **Capture a request.** Use your app, then find its requests in Sniffy's traffic list. Select one to inspect its headers, body, status code, and timing. Search by domain or keyword to find a particular request.
4. **Try a change.** Edit and resend a request, add a rewrite rule, or set a breakpoint to change traffic before it reaches the server or client.

<a id="plugins"></a>

## JavaScript plugins

Plugins let you automate tasks such as adding an auth token, mocking an endpoint, or setting a breakpoint only when a request meets a condition. They can also modify WebSocket and streaming messages.

Open **Plugins**, create a plugin from a template, then edit, save, and enable it. Once enabled, the plugin reloads each time you save. Its settings, URL filters, and logs are available alongside the editor.

<details>
<summary>Example: mock an endpoint</summary>

This plugin returns a JSON response for `/api/demo/profile`. Set its URL allowlist to limit it to a particular domain.

```js
function onRequest(flow) {
  if (flow.path !== '/api/demo/profile') return;

  mock({
    status: 200,
    headers: { 'Content-Type': 'application/json; charset=utf-8' },
    body: JSON.stringify({ name: 'Sniffy', plan: 'pro' }),
  });
}
```

</details>

Hooks are available for requests, responses, WebSocket messages, and streaming messages. Built-in helpers cover encoding, hashing, signing, and persistent storage. See the [plugin helper reference](docs/plugins-helpers.md) (Chinese) for details.

<a id="documentation"></a>

## Documentation

- **Proxy and certificate setup**: Start with [Getting started](#getting-started). Certificate Manager includes installation instructions for each platform.
- **[Plugin helper reference](docs/plugins-helpers.md)** (Chinese): Runtime limits, text and binary payloads, and the available helper APIs.
- **[Outbound TLS verification](docs/outbound-tls.md)** (Chinese): How Sniffy verifies upstream certificates and how to configure trust for internal test servers.

## Headless mode

For server use or automation, run Sniffy in headless mode. The REST API gives you access to sessions, rules, and plugins; WebSocket subscriptions provide live events.

<details>
<summary>Run the server and connect to the API</summary>

Download the headless binary for your platform from [Releases](https://github.com/mintfog/sniffy/releases), or run it from source with Go 1.26+:

```bash
go run ./cmd/sniffy
```

By default, the proxy listens on port `8080` and the management API on `http://127.0.0.1:8888`. Use `-addr` and `-port` to change the proxy's address and port, or `-api-addr` and `-api-port` for the API.

The management API requires a Bearer token. You can supply one through `SNIFFY_API_TOKEN` when starting the server. Otherwise, Sniffy reads it from `sniffy/api_token` in your user configuration directory, creating the file if needed. The startup log includes the file's location.

To query sessions, set `SNIFFY_API_TOKEN` in your shell to the token used by the server, then run:

```bash
curl http://127.0.0.1:8888/api/sessions \
  -H "Authorization: Bearer $SNIFFY_API_TOKEN"
```

Subscribe to events at `/api/ws`. To expose the management API on a non-loopback address, enable HTTPS with `-api-tls-cert` and `-api-tls-key`. If a TLS reverse proxy or VPN already protects the connection, `-allow-insecure-api` permits HTTP.

The desktop interface connects to the local service through Wails.

</details>

## Building from source

Sniffy is built with **Go, Wails v3, React, and TypeScript**. The desktop app and headless server share the capture engine, rule engine, and plugin runtime.

<details>
<summary>Prerequisites and commands</summary>

You'll need Go 1.26+, Node.js 22+, npm, and the desktop dependencies below:

| Platform | Desktop dependencies |
| --- | --- |
| Windows | WebView2; run Bash scripts using Git Bash |
| macOS | System WebView and Xcode Command Line Tools, with CGO enabled |
| Linux | C toolchain, GTK 4, and WebKitGTK 6.0, with CGO enabled |

Clone the repository, build the frontend, and launch the desktop app:

```bash
git clone https://github.com/mintfog/sniffy.git
cd sniffy
bash scripts/build.sh frontend
go run -tags desktop .
```

Run these commands from the repository root. Compiled binaries are written to `dist/`.

```bash
# Build the desktop app for the current platform
bash scripts/build.sh desktop

# Build the headless server for the current or specified platforms
bash scripts/build.sh headless
bash scripts/build.sh headless linux/amd64 darwin/arm64 windows/amd64

# Run tests and frontend checks
go test ./...
npm --prefix web test
npm --prefix web run type-check
npm --prefix web run lint
```

If you use Task, [Taskfile.yml](Taskfile.yml) provides `task dev`, `task desktop`, `task build:all`, and `task test`.

The [release guide](docs/releasing.md) (Chinese) covers version tags, release notes, and automated builds.

</details>

## Contributing

Bug reports and feature requests go in [Issues](https://github.com/mintfog/sniffy/issues). Code fixes, documentation improvements, plugins, and debugging examples are welcome as [pull requests](https://github.com/mintfog/sniffy/pulls).

For bug reports, include your Sniffy version, operating system, and steps to reproduce. Remove credentials and other sensitive data from any logs or captures you attach. Before starting a large change, open an issue to discuss it.

## License

Sniffy is licensed under the [Apache License 2.0](LICENSE).
