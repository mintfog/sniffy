---
title: Headless service
description: Run Sniffy on a server, in a container, or in CI, with authentication, TLS, and remote access.
---

The headless service provides REST and WebSocket APIs for capture, rules, and plugins. Use it on servers, in containers or CI, and for scripted automation.

Rules, plugins, and configuration come from the same configuration directory as the desktop app, so the two can be used interchangeably on one machine.

## Starting it

Using a binary from Releases:

```bash
./sniffy-linux-amd64
```

Or straight from the repository (needs Go 1.26+):

```bash
go run ./cmd/sniffy
```

After startup:

- The proxy listens on `0.0.0.0:8080`
- The management API listens on `http://127.0.0.1:8888`
- The first run generates a root certificate and a management API token; their paths are in the startup log

`Ctrl+C` shuts down gracefully, waiting up to 30 seconds.

## Command-line flags

| Flag                    | Default     | Purpose                                            |
| ----------------------- | ----------- | -------------------------------------------------- |
| `-addr`                 | `0.0.0.0`   | Proxy listen address                               |
| `-port`                 | `8080`      | Proxy listen port                                  |
| `-api-addr`             | `127.0.0.1` | Management API listen address                      |
| `-api-port`             | `8888`      | Management API (HTTP + WebSocket) port             |
| `-api-tls-cert`         | empty       | TLS certificate for the management API             |
| `-api-tls-key`          | empty       | TLS private key for the management API             |
| `-allow-insecure-api`   | off         | Allow the management API to serve plain HTTP on a non-loopback address |
| `-v`                    | off         | Verbose logging                                    |
| `-version`              | —           | Print the version and exit                         |

Precedence is **defaults < `config.json` < explicitly given command-line flags**.

Everything else on the proxy side (upstream proxy, decryption scope, throttling, proxy authentication, …) is configured through `config.json` or `PUT /api/config`, see the [Configuration reference](/en/docs/configuration/).

## Authentication

The management API requires a Bearer token on every listening address, including loopback addresses such as `127.0.0.1`.

The token comes from one of two places:

1. The `SNIFFY_API_TOKEN` environment variable
2. The `api_token` file in the configuration directory, generated if missing

Generated token files are restricted to the current user: POSIX permissions are `0600`, and Windows uses an owner-only DACL.

The token is stored separately in `api_token` and is not read or written through the configuration API.

### Over-broad permissions trigger rotation

If startup detects that other users can read `api_token`, Sniffy tightens its permissions and generates a new token. The starting instance uses the new value, so callers need to read the file again.

If the token can't be handled securely, Sniffy refuses to start — on any address.

### Using the token

```bash
export SNIFFY_API_TOKEN="$(cat ~/.config/sniffy/api_token)"

curl http://127.0.0.1:8888/api/sessions \
  -H "Authorization: Bearer $SNIFFY_API_TOKEN"
```

REST accepts the token only in the `Authorization` header. `/api/ws` additionally accepts a `?token=` query parameter, because some WebSocket clients can't set headers:

```bash
websocat "ws://127.0.0.1:8888/api/ws?token=$SNIFFY_API_TOKEN"
```

When using the query parameter, keep the resulting URL out of logs and out of other people's hands.

## Remote access

Binding the management API to a non-loopback address requires TLS:

```bash
./sniffy-linux-amd64 \
  -api-addr 0.0.0.0 \
  -api-tls-cert /etc/sniffy/api.crt \
  -api-tls-key /etc/sniffy/api.key
```

A non-loopback management listener without TLS is refused by default. Deployments protected by a TLS reverse proxy or VPN can allow HTTP with this flag:

```bash
./sniffy-linux-amd64 -api-addr 127.0.0.1 -allow-insecure-api
```

With HTTPS on, the WebSocket URL becomes `wss://`.

Sniffy stops startup and reports an error if the management port cannot bind or the TLS certificate cannot be loaded.

### Methods and paths

Beyond authentication, two more rules apply to every call:

- **Request methods** — endpoints accept only their documented methods. Other methods return 405 with an `Allow` header listing those supported. State-changing operations don't accept `GET` or `HEAD`.
- **Request paths** — undefined subpaths return 404, for example `/api/sessions/{id}/typo`.

A `/api/ws` upgrade from a browser page also has to be same-origin with the management port, see [Authentication and security](/en/docs/api/auth/).

## Deployment patterns

### systemd

```ini
[Unit]
Description=Sniffy proxy
After=network.target

[Service]
ExecStart=/usr/local/bin/sniffy -addr 0.0.0.0 -port 8080
Environment=SNIFFY_API_TOKEN=%your-token%
User=sniffy
Restart=on-failure

[Install]
WantedBy=multi-user.target
```

When injecting the token through the environment, put it in a systemd `EnvironmentFile` with mode `0600` rather than inline in the unit file.

### Docker

```dockerfile
FROM debian:stable-slim
COPY sniffy-linux-amd64 /usr/local/bin/sniffy
EXPOSE 8080 8888
ENTRYPOINT ["/usr/local/bin/sniffy", "-api-addr", "0.0.0.0", "-allow-insecure-api"]
```

```bash
docker run --rm -p 8080:8080 -p 8888:8888 \
  -e SNIFFY_API_TOKEN=... \
  -v sniffy-config:/root/.config/sniffy \
  sniffy
```

Mount the configuration directory as a volume so the root certificate and rules survive container rebuilds. `-api-addr 0.0.0.0` is there so the host can reach it — only expose port 8888 to a trusted network, or put TLS termination in front of it.

### CI

Run API tests in a pipeline and inspect what was actually sent:

```bash
./sniffy -port 8080 &
export SNIFFY_API_TOKEN="$(cat ~/.config/sniffy/api_token)"
export http_proxy=http://127.0.0.1:8080 https_proxy=http://127.0.0.1:8080

npm test

curl -s -X POST http://127.0.0.1:8888/api/export \
  -H "Authorization: Bearer $SNIFFY_API_TOKEN" \
  -H "Content-Type: application/json" \
  -d '{"hosts":["api.example.com"]}' > sessions.json
```

For HTTPS the program under test has to trust the root certificate, see [HTTPS and the root certificate](/en/docs/https/).

## Live events

Subscribe to `/api/ws` to track capture events in real time:

```bash
websocat "ws://127.0.0.1:8888/api/ws?token=$SNIFFY_API_TOKEN"
```

Messages are `{"type": "...", "payload": ...}`. The list of event types is in the [Management API reference](/en/docs/api/events/).

Slow clients may miss events or be disconnected. After reconnecting, query `/api/sessions` for sessions still retained by Sniffy. Retention is limited by `maxFlows`.

## Logs

Logs go to `logs/` in the configuration directory, rotated daily and kept for 7 days. `-v` turns on verbose logging.

## Related

- [Management API reference](/en/docs/api/) — every endpoint
- [Configuration reference](/en/docs/configuration/) — `config.json` fields
- [HTTPS and the root certificate](/en/docs/https/) — trusting the certificate in the program under test
