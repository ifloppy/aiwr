# Web UI

The native Go server includes a small, dependency-free browser workbench. It
uses the same-origin HTTP API; no Node build, frontend server, CDN, or model is
required.

## Start it

```bash
aiwr serve
```

Open [http://127.0.0.1:8765/](http://127.0.0.1:8765/). The UI can:

- accept pasted text or one local file;
- call `/inspect`, `/detect`, and `/clean`;
- show the JSON report and configured optional backends from `/capabilities`;
- display cleaned text and download a new output file.

The default `aiwr serve` startup needs no API key and listens only on the local
machine (`127.0.0.1`).

The browser sends file bytes as base64 to the current aiwr server. The default
processing choice is `layer_a_only`, so the UI does not call a rewrite model or
remote backend unless that behavior is later added as an explicit option to
the API request. Cleaning produces a download; it never overwrites the input
file in the browser.

## Authentication

Only configure a bearer key when you intentionally expose the API beyond the
local user:

```bash
WATERMARKS_SERVER_API_KEY=change-me aiwr serve
```

The page at `/` and its static assets remain available, but API requests are
then protected. The minimal local UI is intended for the default no-key mode;
authenticated clients should send `Authorization: Bearer ...` directly or use
an authenticated API client. The key is never put in the URL by aiwr.

The server listens on `127.0.0.1:8765` by default. Before binding or proxying it
to another host, configure authentication, TLS, and the access policy for the
files being submitted. The UI intentionally does not accept an arbitrary API
base URL, which keeps it same-origin and avoids turning the page into a proxy
for unrelated services.

## API and limitations

The UI is a convenience client, not a replacement for the machine-readable
contract. Use [`/openapi.json`](http://127.0.0.1:8765/openapi.json) for client
generation and the [user guide](USER_GUIDE.md) for request limits and option
semantics. For directories, batches, Layer B strategies, in-place changes,
SARIF, or reproducible automation, use the CLI or call the API directly.

The UI reports what the current service exposes; an unavailable scorer or
optional tool is not installed by opening the page. Cleaning remains
best-effort and cannot prove human authorship or guarantee that a private,
keyed, pixel-domain, or waveform signal is gone.
