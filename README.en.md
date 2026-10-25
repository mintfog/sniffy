<p align="center">
  <img src="web/src/assets/sniffy-mark.png" width="112" alt="Sniffy">
</p>

<h1 align="center">Sniffy</h1>

<p align="center">
  <a href="README.md">中文</a> · <strong>English</strong>
</p>

<p align="center">
  <strong>An open-source traffic capture and debugging proxy for Windows, macOS, and Linux</strong><br>
  Supports HTTP/HTTPS, WebSocket, gRPC, and SSE
</p>

<p align="center">
  <a href="https://gosniffy.com/en/download/"><img src="https://img.shields.io/github/v/release/mintfog/sniffy?include_prereleases&amp;style=flat-square&amp;label=release&amp;color=087F8C" alt="Latest release"></a>
  <a href="https://github.com/mintfog/sniffy/actions/workflows/test.yml?query=branch%3Amain"><img src="https://img.shields.io/github/actions/workflow/status/mintfog/sniffy/test.yml?branch=main&amp;style=flat-square&amp;label=tests" alt="Test status"></a>
  <img src="https://img.shields.io/badge/platform-Windows%20%7C%20macOS%20%7C%20Linux-334155?style=flat-square" alt="Windows / macOS / Linux">
  <a href="LICENSE"><img src="https://img.shields.io/badge/license-Apache%202.0-334155?style=flat-square" alt="Apache License 2.0"></a>
</p>

<p align="center">
  <a href="https://gosniffy.com/en/">Website</a> ·
  <a href="https://gosniffy.com/en/download/"><strong>Download Sniffy</strong></a> ·
  <a href="#features">Features</a> ·
  <a href="https://gosniffy.com/en/docs/desktop/">Getting Started</a> ·
  <a href="https://gosniffy.com/en/docs/plugins/">Plugins</a> ·
  <a href="https://gosniffy.com/en/docs/">Documentation</a> ·
  <a href="https://github.com/mintfog/sniffy/issues">Report an Issue</a>
</p>

---

Sniffy runs on Windows, macOS, and Linux. It captures HTTP/HTTPS requests and responses, bidirectional WebSocket messages, and streaming sessions such as gRPC and SSE.

When debugging an HTTP endpoint, replay a failing request to reproduce a bug, or return a mock response to test how your client handles it. You can also pause a request or response at a breakpoint, edit its contents, and let it continue.

## Installation

**[Download Sniffy →](https://gosniffy.com/en/download/)** · [Installation guide](https://gosniffy.com/en/docs/install/)

<a id="features"></a>

## Features

| Feature | What you can do |
| --- | --- |
| **Inspect traffic** | View HTTP / HTTPS request and response headers and bodies. Search, filter, and mark captured sessions. |
| **Send requests** | Compose new requests, or edit and resend captured ones. Import from cURL or copy requests as cURL commands. |
| **Apply rules** | Rewrite, redirect, block, or mock traffic that matches your conditions. |
| **Set breakpoints** | Pause a request or response, edit its contents, and resume it manually. |
| **WebSocket** | Inspect messages sent between the client and server. |
| **gRPC / SSE** | View individual gRPC messages, SSE events, and other streaming data. |
| **Write plugins** | Write JavaScript in the built-in editor for custom logic, scope plugins to specific URLs, hot-reload on save, and view live logs. |
| **Export sessions** | Save HTTP sessions as HAR or export traffic data as JSON. |

<a id="getting-started"></a>

## Getting started

See the [desktop quick start](https://gosniffy.com/en/docs/desktop/) and [HTTPS and root certificate guide](https://gosniffy.com/en/docs/https/) for full setup instructions.

1. **Set the proxy**: Open Sniffy and enable the system proxy, or configure your client to use `127.0.0.1:8080` as its HTTP / HTTPS proxy. If you changed the listening port in Settings, use that port instead.
2. **Trust the certificate**: To decrypt HTTPS traffic, open Certificate Manager and follow the prompt to install and trust Sniffy's root certificate. To capture HTTPS traffic from another device, install and trust the certificate on that device as well.
3. **Capture traffic**: Send requests from your app and select them in Sniffy's traffic list to inspect headers, body, status code, and timing. Filter by domain or keyword to narrow down the list.
4. **Modify and test**: Resend a captured request, add a rewrite or mock rule, or set a breakpoint to test how your application handles different responses.

<a id="plugins"></a>

## Plugins

When built-in rules aren't enough, you can write JavaScript plugins directly in the app. Plugins can inject authentication headers, build dynamic mock responses, trigger breakpoints conditionally, or modify WebSocket and streaming messages.

Open the **Plugins** view, create a plugin from a built-in template, then edit, save, and enable it. Enabled plugins hot-reload whenever you save, with settings, URL allowlists, and logs available in the same view.

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

Hooks run on requests, responses, WebSocket messages, and streaming messages. The runtime includes helpers for encoding, hashing, signing, and persistent storage. See the [plugin documentation](https://gosniffy.com/en/docs/plugins/).

<a id="documentation"></a>

## Documentation

- **Proxy and certificate setup**: See [Routing traffic](https://gosniffy.com/en/docs/capture/) and [HTTPS and the root certificate](https://gosniffy.com/en/docs/https/).
- **[Plugin documentation](https://gosniffy.com/en/docs/plugins/)**: Runtime limits, text and binary payloads, and helper APIs.
- **[Outbound TLS verification](https://gosniffy.com/en/docs/https/#outbound-certificate-verification)**: How Sniffy verifies upstream certificates and how to configure trust for internal test servers.
- **[Headless deployment](https://gosniffy.com/en/docs/headless/) and [Management API](https://gosniffy.com/en/docs/api/)**: Startup, authentication, remote access, and API reference.

## Headless mode

For servers or automated workflows, run Sniffy in [headless mode](https://gosniffy.com/en/docs/headless/). You can manage sessions, rules, and plugins over the REST API and subscribe to live events over WebSocket.

<details>
<summary>Run the server and connect to the API</summary>

Download the [headless binary](https://gosniffy.com/en/docs/install/#headless-service) for your platform, or run it from source with Go 1.26+:

```bash
go run ./cmd/sniffy
```

The proxy defaults to `:8080`; use `-port` to change its port. The `-addr` flag accepts only wildcard addresses (empty, `*`, `0.0.0.0`, or `::`). The management API defaults to `http://127.0.0.1:8888`; use `-api-addr` and `-api-port` to change its address and port.

The management API requires a Bearer token. You can supply one through `SNIFFY_API_TOKEN` when starting the server. Otherwise, Sniffy reads it from `sniffy/api_token` in your user configuration directory, creating the file if needed. When Sniffy generates a token, it logs the file's location.

To query sessions, set `SNIFFY_API_TOKEN` in your shell to the token used by the server, then run:

```bash
curl http://127.0.0.1:8888/api/sessions \
  -H "Authorization: Bearer $SNIFFY_API_TOKEN"
```

Subscribe to events at `/api/ws`. To expose the management API on a non-loopback address, enable HTTPS with `-api-tls-cert` and `-api-tls-key`. If a TLS reverse proxy or VPN already protects the connection, `-allow-insecure-api` permits HTTP.

The desktop interface connects to the local service through Wails.

</details>

## Building from source

Sniffy is built with Go, Wails v3, React, and TypeScript. The desktop app and headless server share the same capture engine, rule engine, and plugin runtime.

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

</details>

## Contributing

Report bugs or request features in [Issues](https://github.com/mintfog/sniffy/issues). [Pull requests](https://github.com/mintfog/sniffy/pulls) for code fixes, documentation improvements, plugins, and debugging examples are welcome.

When reporting a bug, include your Sniffy version, operating system, and steps to reproduce, and strip any credentials or sensitive data from attached logs or captures. For larger changes, open an issue first to discuss the approach.

## Community

- [LINUX DO](https://linux.do/) — A new ideal community.
- [V2EX](https://www.v2ex.com/) — A community of creative people.

## License

Sniffy is licensed under the [Apache License 2.0](LICENSE).
