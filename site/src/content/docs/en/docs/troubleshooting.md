---
title: Troubleshooting
description: What to check when nothing is captured, the certificate isn't trusted, decryption fails, or a plugin has no effect.
---

## Nothing is captured at all

Check the proxy status and client configuration in this order:

1. **Check the listener** — the proxy bar should show Listening on :8080. If the port is in use, choose another port.
2. **Verify the client proxy configuration** with this request:
   ```bash
   curl -v --proxy http://127.0.0.1:8080 http://example.com
   ```
   If the request appears, it reached Sniffy successfully. Check whether the target application uses the same proxy address.
3. **Check the port** — the client must use the port Sniffy is listening on.
4. **Check recording status** — when paused, the proxy keeps listening but does not record new traffic.

### The system proxy is on but one program isn't captured

Not every program reads the system proxy setting. Common exceptions:

- Command-line tools written in Go or Rust usually only read the `HTTP_PROXY` / `HTTPS_PROXY` environment variables.
- Java programs need system properties like `-Dhttp.proxyHost`.
- Node.js `fetch` doesn't use a proxy unless you configure `undici`'s ProxyAgent.
- Some applications have built-in bypass rules, or use QUIC / HTTP3 to go out directly.

See [Routing traffic](/en/docs/capture/#configuring-one-program).

### Only CONNECT shows up, never the actual requests

HTTPS traffic may be passing through a tunnel. Check certificate trust and decryption scope as described below.

## HTTPS isn't decrypted

### Check root certificate trust

Visit an HTTPS site through Sniffy and inspect its certificate in the browser. With the default CA, an issuer of **Sniffy Root CA** indicates interception. If the origin certificate is shown, check proxy settings and decryption scope. If a certificate error appears, check whether the client trusts Sniffy’s root.

### The certificate is installed and it still doesn't decrypt

Check these additional possibilities:

1. **Root trust is disabled on iOS** — after installing the profile, enable trust under Settings - General - About - Certificate Trust Settings.
2. **The browser has not loaded the new trust settings** — restart it after installing the certificate.
3. **The decryption scope excludes it** — check the scope and lists under Settings - HTTPS decryption.
4. **The Linux browser uses a separate certificate store** — some browsers need the certificate in an NSS store. If `certutil` is unavailable or the store is locked during installation, import it through the browser settings.
5. **The Android app does not trust user CAs** — apps targeting API 24 or later trust only system CAs by default; behavior depends on the app's configuration. See [Phones and remote clients](/en/docs/mobile/#4-check-whether-your-app-is-supported).
6. **The target pins its certificate** — see below.

### Certificate pinning

The application hard-codes the certificate or public key it expects and refuses anything else, including Sniffy's. It shows up as one domain failing consistently while everything else works.

Choose an approach based on whether you maintain the app:

- Add the domain to the **decryption denylist** so it tunnels through. The app works, and you still see the destination and the traffic volume.
- When debugging your own app, disable pinning in the debug build.

### Outbound certificate errors

Sniffy fully verifies certificates when connecting to the real server. A self-signed or expired origin certificate stops forwarding, and the error is recorded.

For an internal CA, import it into the trust store of the system running Sniffy and restart. Replace expired certificates or certificates with a hostname mismatch on the server. For controlled debugging, see host-specific exceptions in the [Configuration reference](/en/docs/configuration/#outbound-tls-exceptions).

## A phone can't reach the proxy

1. **Check network connectivity** — the device must be able to reach the machine running Sniffy. Client isolation on corporate or public Wi-Fi may block this.
2. **Right address?** — it has to be the **LAN address** of the machine running Sniffy, not `127.0.0.1`. The network menu in the proxy bar lists the options; with both Wi-Fi and Ethernet connected, pick the one on the phone's subnet.
3. **Firewall open?** — Windows prompts on first bind; if you dismissed it, allow the app in firewall settings. Check local firewall rules on macOS and Linux too.
4. **Check proxy authentication** — Android's manual Wi-Fi proxy settings typically cannot supply credentials, so authentication may produce 407 responses. Test with authentication disabled on a trusted network.

## System proxy issues

### No network after Sniffy exits

The system proxy still points at a port that no longer exists. Start Sniffy and click "Disable system proxy", or turn the proxy off in system settings.

### Can't enable the system proxy on Linux

Only GNOME-based desktops are supported (through `gsettings`). On other desktops, configure the proxy manually or use environment variables.

### The system proxy is on but has no effect on macOS

`networksetup` only changes **currently active** network services. If you just switched networks (Wi-Fi to Ethernet, say), click the enable button again.

## Rewrites have no effect

### A rule doesn't fire

1. Confirm the rule is enabled.
2. Check the conditions. `url_host` includes the port; `url_path` excludes the query string. Test matching with `url` + `contains`, then narrow the scope.
3. Check execution order. An earlier `block` or `auto_respond` action stops later rules.
4. Response-only conditions (`response_status`, `response_header`) have no value during the request phase and count as not matching — pairing one with a request-editing action means the action never runs.

### A plugin doesn't fire

1. Confirm the plugin is enabled. Load failures appear with error information in the plugin list.
2. Check the URL allowlist and denylist. Denylist matches skip the plugin; an empty allowlist imposes no further matching restriction.
3. Add `console.log` in the hook and check the log panel to confirm it is called.
4. **Check execution time** — each hook call has a default limit of 100 ms. On timeout its edits are not applied. Check the logs and reduce expensive operations.
5. **Check script exceptions** — errors appear in the log panel. Edits made before an exception may remain, so review the script's execution order.

### A plugin can't read the response body

If the response has content but `flow.response.body` is empty, check the binary field `bodyB64` first. These response types do not provide a full body:

- **Streaming responses** (SSE / gRPC / NDJSON) — rewrite them in `onStreamMessage`.
- **Pass-through responses** (media types / 206 / over the size threshold) — bodies are not fully buffered. Disable `largeBodyPassthrough` to edit them in full.

See [JavaScript plugins](/en/docs/plugins/#when-the-full-body-isnt-available).

## A header I changed wasn't sent

Some headers are recalculated or removed when sent. For example, `Content-Length` is calculated from the body length. Check the outbound message preview or the breakpoint editor for notices.

## Breakpoint issues

### A request was released and I never clicked anything

Each paused entry has a 5-minute deadline, after which it is **released unchanged** — your unsaved edits don't apply, and the list marks it as auto-released. Click "Extend" when you need longer.

### Too many requests paused by global breakpoints

A web page can send many requests at once, all paused by a global breakpoint. Use Release all, then narrow the scope with URL rules.

### A breakpoint didn't trigger

Up to 100 flows can be paused at once. New flows skip the breakpoint when the limit is reached; resolve existing entries first.

## Management API issues

### 401 unauthorized

Check whether the token is missing or differs from the value used by the running instance. Loopback connections also require authentication. This example reads the token from the default Linux configuration directory:

```bash
cat ~/.config/sniffy/api_token
```

If startup logs report overly broad API token file permissions, the token was rotated for this startup. Read the file again and update client credentials.

### 403 cross-site request rejected

When connecting to `/api/ws` from a browser page, the upgrade request's `Origin` must match `Host`, or it is rejected. Use `127.0.0.1:8888` directly as the WebSocket address from a local page, rather than going through another hostname or port.

### 405 method not allowed

Check the method against the endpoint documentation. The `Allow` response header lists supported methods. Reading and writing at the same path may use different methods: plugin source uses `GET` to read and `PUT` to write.

### 404 unknown action

Check for misspellings or extra path segments, such as `/api/sessions/{id}/typo`, and use the documented endpoint path.

### It refuses to bind a non-loopback address

A non-loopback management listener without TLS is refused by default. Enable HTTPS with `-api-tls-cert` and `-api-tls-key`. Deployments protected by a TLS reverse proxy or VPN can use `-allow-insecure-api`.

## Performance and resources

### The list stutters and memory climbs

- Lower `maxFlows`; the oldest sessions are dropped past the capacity.
- Check that `largeBodyPassthrough` is enabled. Disabling it buffers large responses that would otherwise pass through, increasing memory use.
- Narrow the decryption scope: hosts whose content you don't need are much cheaper as tunnels.
- Clear traffic periodically.

### The cache directory keeps growing

It holds on-disk copies of large response bodies and can be deleted entirely, see the [Configuration reference](/en/docs/configuration/#cache-directory).

## Where are the logs

In `logs/` in the configuration directory, rotated daily and kept for 7 days. On headless, `-v` turns on verbose logging.

## Still stuck

Open an issue on [GitHub Issues](https://github.com/mintfog/sniffy/issues) with:

- The Sniffy version (Settings - About, or `sniffy -version`)
- Your operating system and version
- Steps to reproduce
- Relevant log excerpts, redacted as needed
