---
title: Authentication and security
description: Where the management API Bearer token comes from, how it rotates, TLS requirements, and the CSRF and method-allowlist boundaries.
---

## Authentication requirements

**A Bearer token is required on every listen address, loopback included.**

The management API can read captured traffic and change runtime settings. Other users and processes on the same machine may access a loopback port, so authentication is required there too.

## Where the token comes from

In priority order:

1. The `SNIFFY_API_TOKEN` environment variable
2. The `api_token` file in the configuration directory

With neither present, Sniffy generates one at startup and writes `api_token`. The path is printed in the startup log:

```
已生成管理 API token 并保存到 /home/you/.config/sniffy/api_token;
请求需携带 Authorization: Bearer <token>,WebSocket 可用 ?token=
```

| Platform | File permissions                             |
| -------- | -------------------------------------------- |
| POSIX    | `0600`                                       |
| Windows  | owner-only DACL (set via `x/sys/windows`)    |

When instances start concurrently using the same configuration directory, token file creation is coordinated so they read the same saved value.

### Token storage

The token is stored separately in `api_token`. Reading or writing `config.json` through the configuration API does not expose or update it.

## Over-broad permissions trigger rotation

If, at startup, `api_token` is readable by group or other, the old value may already have leaked, so Sniffy:

1. Generates a new token for the starting instance;
2. Tightens the file permissions;
3. Notes the rotation in the log.

```
检测到 API token 文件权限过宽(旧值可能已泄漏),已轮换为新凭证,旧凭证即时失效
```

Token generation, rotation, and permission changes are coordinated with a cross-process file lock that is released when the process exits.

If token generation, reading, or permission handling fails, Sniffy refuses to start and logs the reason.

:::caution
After a rotation is logged, read the token file again and update client credentials. The old value returns 401 when used with the newly started instance.
:::

## Sending the token

### REST

The `Authorization` header only:

```bash
export SNIFFY_API_TOKEN="$(cat ~/.config/sniffy/api_token)"

curl http://127.0.0.1:8888/api/sessions \
  -H "Authorization: Bearer $SNIFFY_API_TOKEN"
```

The `Bearer ` prefix is case-insensitive. Surrounding token whitespace is trimmed; the token itself must match exactly.

### WebSocket

Besides the header, `/api/ws` **additionally** accepts a query parameter — the browser `WebSocket` constructor can't set headers:

```bash
websocat "ws://127.0.0.1:8888/api/ws?token=$SNIFFY_API_TOKEN"
```

```js
const ws = new WebSocket(`ws://127.0.0.1:8888/api/ws?token=${token}`);
```

The query parameter works only on `/api/ws`; using it elsewhere gets a 401.

:::caution
Tokens in URLs may be recorded in shell history, process listings, or access logs. Prefer `Authorization` when headers are supported.
:::

## Transport security

| Bind address  | TLS  | Result                                                     |
| ------------- | ---- | ---------------------------------------------------------- |
| Loopback      | no   | Starts normally                                            |
| Loopback      | yes  | Starts normally, served over `https://` and `wss://`       |
| Non-loopback  | yes  | Starts normally                                            |
| Non-loopback  | no   | **Refuses to start**, unless `-allow-insecure-api` is given |

```bash
./sniffy \
  -api-addr 0.0.0.0 \
  -api-tls-cert /etc/sniffy/api.crt \
  -api-tls-key /etc/sniffy/api.key
```

Both TLS flags must be supplied together; giving only one is a fatal error.

Use `-allow-insecure-api` for deployments protected by a TLS reverse proxy or VPN. It logs a warning on startup and permits plaintext HTTP, so restrict the management port to the protected network.

### Startup checks

Startup binds the management port and loads TLS certificates. If the port is in use or a certificate cannot be read or parsed, Sniffy stops startup and reports an error.

## Other boundaries

The following checks determine whether an endpoint accepts a request.

### CSRF and DNS rebinding

Upgrade requests to `/api/ws` must be same-origin with the management port: if `Origin` is present, its host has to match `Host`, or the upgrade is rejected with a 403. When subscribing to events from a browser page, use the management port's own address as the WebSocket URL.

If you embed the management API in your own program and configure no token, every request goes through a same-origin check first:

- `Host` must be a loopback address or `localhost`
- `Sec-Fetch-Site` may only be empty, `same-origin`, or `none`
- If `Origin` is present, its host must match `Host`

Failed checks return `403 cross-site request forbidden`.

### Method allowlists

Each endpoint accepts only its documented methods. Other methods return `405` with an `Allow` header listing supported methods. State-changing operations do not accept `GET` or `HEAD`.

Some paths support both reading and writing, distinguished by the request method:

| Path                       | GET             | PUT                            |
| -------------------------- | --------------- | ------------------------------ |
| `/api/plugins/{id}/source` | Read the script | Write it and hot-reload        |
| `/api/config`              | Read config     | Write config (PUT/POST)        |

### Extra path segments

Undefined subpaths, such as `/api/sessions/{id}/typo`, return `404 unknown action`. Use the full path documented for the endpoint.

## Deployment checklist

For remote deployments, check the following configuration:

- [ ] The token is injected via environment or a permission-restricted file, not hard-coded into an image or unit file
- [ ] TLS is enabled for non-loopback binds, or a TLS reverse proxy is confirmed in front
- [ ] The management port is accessible only from a trusted network
- [ ] Logs and monitoring don't record URLs containing `?token=`
- [ ] The configuration directory is on a persistent volume, so container rebuilds don't regenerate the token and root certificate every time

## Related

- [Conventions and errors](/en/docs/api/conventions/) — what 401, 403, and 405 each mean
- [Headless service](/en/docs/headless/) — flags and deployment
- [Troubleshooting](/en/docs/troubleshooting/#management-api-issues) — working through authentication failures
