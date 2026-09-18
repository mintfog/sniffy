---
title: Conventions and errors
description: The management API's response envelope, pagination, status code semantics, and general calling rules.
---

This page covers response formats, pagination, errors, and update semantics. See each endpoint for its fields and limits.

## Response envelope

Most JSON endpoints use this response envelope. Pagination, file downloads, and streaming exports use the formats described below:

```json
{
  "data": { },
  "success": true,
  "timestamp": "2026-09-17T10:00:00+08:00"
}
```

| Field       | Type    | Meaning                                                      |
| ----------- | ------- | ------------------------------------------------------------ |
| `data`      | any     | The payload. Omitted entirely when there is none (a successful delete, say) |
| `success`   | boolean | Whether it worked                                            |
| `message`   | string  | Failure reason; omitted on success                           |
| `timestamp` | string  | Server time, RFC3339                                         |

On failure:

```json
{
  "success": false,
  "message": "session not found",
  "timestamp": "2026-09-17T10:00:00+08:00"
}
```

Check the HTTP status before parsing the response in its expected format. Errors from intermediaries such as reverse proxies may use a different format.

### Endpoints without the envelope

| Endpoint                       | Returns                                      |
| ------------------------------ | -------------------------------------------- |
| `/api/certificate/ca`          | A PEM file, `application/x-pem-file`         |
| `/api/certificate/ios-profile` | A profile, `application/x-apple-aspen-config` |
| `/api/certificate/export`      | A certificate file, format per request       |
| `/api/sessions/{id}/body/raw`  | Raw body bytes, Range supported              |
| `/api/export`                  | A streamed JSON array                        |
| Paginated list endpoints               | Pagination object, described below      |

## Pagination

Session, WebSocket session, streaming session, and rewrite rule lists use the pagination object below. Plugin and breakpoint lists use the formats documented for their endpoints:

```json
{
  "data": [],
  "total": 128,
  "page": 1,
  "pageSize": 50,
  "hasNext": true,
  "hasPrev": false
}
```

| Query parameter | Default | Meaning              |
| --------------- | ------- | -------------------- |
| `page`          | `1`     | 1-based page number  |
| `pageSize`      | `50`    | Items per page       |

Both must be positive integers. Invalid values (negative, zero, non-numeric) are ignored and fall back to the default rather than erroring.

For a page beyond the available range, `data` is empty and `total` still reports the total count.

Walking everything:

```bash
page=1
while :; do
  resp=$(curl -s "$SNIFFY_API/api/sessions?page=$page&pageSize=100" \
           -H "Authorization: Bearer $SNIFFY_API_TOKEN")
  echo "$resp" | jq -c '.data[]'
  [ "$(echo "$resp" | jq -r '.hasNext')" = "true" ] || break
  page=$((page + 1))
done
```

:::caution
The session list changes during capture, so pagination can repeat or miss entries. Use [`/api/export`](/en/docs/api/export/) for bulk retrieval to reduce pagination effects. It reads sessions individually and does not provide a transactional snapshot either.
:::

## Status codes

| Code | Meaning                        | Typical cause                                              |
| ---- | ------------------------------ | ---------------------------------------------------------- |
| 200  | Success                        | —                                                          |
| 400  | Something's wrong with the request | Malformed JSON, missing required field, out-of-range value, unknown field |
| 401  | Authentication failed          | Token missing, wrong, or just rotated                      |
| 403  | Cross-site request rejected    | A WebSocket upgrade whose `Origin` doesn't match `Host`    |
| 404  | No such resource               | Unknown id, or unknown / extra path segments               |
| 405  | Method not allowed             | Not on this endpoint's allowlist; response carries `Allow` |
| 413  | Request body too large         | Over this endpoint's limit                                 |
| 500  | Server-side failure            | Certificate generation failed, disk write failed, …        |
| 501  | Capability unavailable         | This assembly has no plugin system, breakpoint manager, or certificate management |
| 503  | Composer not wired up          | `/api/compose` has no sender attached                      |

### Body limits

Exceeding a limit returns 413, as distinct from 400: the former means trimming content, the latter means fixing a field or the formatting.

| Endpoint                   | Limit                                  |
| -------------------------- | -------------------------------------- |
| `/api/compose`             | 8 MiB                                  |
| `/api/compose/ws/*/send`   | 12 MiB                                 |
| `/api/breakpoints/*`       | 9 MiB                                  |
| `/api/certificate/export`  | 64 KiB                                 |
| `/api/certificate/import`  | 11 MiB (the certificate file itself 10 MiB) |
| `/api/export`              | 64 KiB                                 |

### 500 errors and retries

`500` indicates a server-side failure and does not guarantee rollback. Check the resource state and whether the operation is idempotent before retrying. For example, `DELETE /api/plugins/{id}` may stop the plugin but fail to remove its directory, allowing it to load again after a restart.

## Strict decoding

These endpoints reject unknown fields with 400:

| Endpoint                         | Details                                                  |
| ------------------------------ | --------------------------------------------------------------- |
| `/api/breakpoints/{id}/resume` | Accepts an edit patch. Paused snapshots use base64 bodies; patch `body` fields use text. Submit the patch schema. |
| `/api/export` | Rejects unknown filter fields to avoid exporting with unintended criteria. |

Both also require the body to be **exactly one JSON value**; trailing content is an error.

## Patch semantics

`/api/config` and `/api/plugins/{id}/manifest` accept partial objects — send only what changes:

```bash
curl -X PUT $SNIFFY_API/api/config \
  -H "Authorization: Bearer $SNIFFY_API_TOKEN" \
  -H "Content-Type: application/json" \
  -d '{"throttle": true}'
```

Fields you leave out keep their values. This differs from `PUT /api/intercept/rules/{id}`, which is a **whole replacement** — anything omitted becomes a zero value.

Array fields replace rather than append: `{"decryptAllow": ["a.com"]}` leaves only `a.com` in the list.

## The header byte side channel

Throughout the session and breakpoint interfaces, any header set may come with a `headersB64` side channel.

Header values are allowed to contain non-UTF-8 bytes — most often a Latin-1 filename in `Content-Disposition` — while JSON strings must be valid UTF-8. Such bytes become `U+FFFD` in the main field, with the original bytes in the side channel.

| Situation                         | What to do                                                        |
| --------------------------------- | ----------------------------------------------------------------- |
| Just reading and displaying       | Use the main field, ignore the side channel                       |
| You need byte fidelity            | Where a side-channel entry is non-empty, it wins                   |
| Writing back (breakpoints, composer) | The side channel is a **full list aligned by index** with the main one, empty strings at text positions, and the lengths must match |

## Time and encoding

- All timestamps are RFC3339 strings.
- Duration fields (`duration`, `responseTime`) are integer milliseconds; `uptime` is seconds.
- Request bodies are UTF-8 JSON, except `/api/certificate/import` which uses `multipart/form-data`.
- Responses default to `Content-Type: application/json`.

## Concurrency

There is no optimistic locking and no ETag. When two clients change the same rule or configuration at once, the later write wins.

Where you need serialization — several CI jobs sharing one Sniffy, for instance — add the lock on the calling side.

## Related

- [Authentication and security](/en/docs/api/auth/) — where 401 and 403 come from
- [Management API overview](/en/docs/api/) — the endpoint index
