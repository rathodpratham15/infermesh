# InferMesh Test Console

A test console and metrics inspector for the **InferMesh** LLM inference gateway.

This tool is designed to benchmark and inspect gateway behavior:
- Which backend served each request
- Latency (browser-measured round-trip time)
- Time to First Token (TTFT)
- Tokens per second (calculated honestly from server-reported usage)
- Backend health and concurrency telemetry
- SSE, WebSocket, and non-streaming HTTP transports
- Backend pinning (`X-InferMesh-Pin-Backend` header and `?backend=` query param)
- Multi-line System Prompt sent as the first message (`role: 'system'`) in the `messages` array for all transports
- Session Request Log tab with table of past requests and Export JSON functionality

---

## Requirements

Node **20.19+ or 22.12+** (Vite 8 requires it; older Node silently skips a native dependency and the build fails with
`Cannot find module './rolldown-binding.wasi.cjs'`). `.nvmrc` pins 22.

```bash
npm install
npm run dev       # http://localhost:5173
npm test          # unit tests (vitest)
npm run lint      # type check
npm run build
```

---

## Environment Variables

| Variable | Default | Description |
| :--- | :--- | :--- |
| `VITE_GATEWAY_URL` | `http://localhost:8080` | Base URL of the InferMesh gateway. In `npm run dev` with the default URL, HTTP calls go through the Vite proxy (`/v1`, `/readyz`, `/healthz`), so no CORS is needed. Any other URL, or a production build, is called directly and needs `CORS_ALLOWED_ORIGINS` on the gateway. An empty value means "same origin". |
| `VITE_MOCK` | `true` | `true` enables the built-in mock gateway (simulated backends `mock-a`, `mock-b`, canned replies). Set `false` to use a real gateway. |

---

## How to Run

### Mock mode (default, no gateway needed)
`npm run dev`. A persistent banner shows that the data is simulated.

### Against a local InferMesh gateway
1. Start the gateway, for example `make up` at the repo root (its Compose defaults already allow `http://localhost:5173`),
   or run it yourself with:
   ```bash
   CORS_ALLOWED_ORIGINS=http://localhost:5173 WS_ALLOWED_ORIGINS=localhost:5173 go run ./cmd/gateway
   ```
   HTTP calls use the Vite proxy in dev, so `WS_ALLOWED_ORIGINS` is the setting that matters for the WebSocket transport.
2. Run the console against it:
   ```bash
   VITE_MOCK=false npm run dev      # or: make frontend-dev   (from the repo root)
   ```
3. Open `http://localhost:5173`. When connected to a real gateway the model selector is filled from `GET /v1/models`
   (free text if the list is empty), and the first listed model is selected automatically.

---

## API Specifications Implemented

1. **POST `/v1/chat/completions`**:
   - Non-streaming: JSON response with `choices[0].message.content` and `usage`.
   - Streaming (`stream: true`): Server-Sent Events parsed via hand-crafted `ReadableStream` parser.
     - Role chunk (`choices[0].delta.role`) ignored for TTFT.
     - Content chunks (`choices[0].delta.content`) establish TTFT upon the first content token.
     - In-band error handling (`data: {"error": ...}`).
     - `data: [DONE]` terminal signal.
     - `X-Request-ID` and `X-InferMesh-Backend` response headers.
     - Strict abort via `AbortController` when clicking Stop.

2. **WebSocket `ws(s)://<gateway>/v1/chat/stream`**:
   - Single JSON payload sent on connection.
   - Handled server frames: `start`, `token`, `done`, `error`.
   - Captures browser TTFT and server-reported `ttft_ms`.
   - Socket closure terminates request.

3. **Fleet Telemetry**:
   - `GET /v1/backends`: Polled every 3 seconds for queue depth, active/max concurrency, and EWMA latency.
   - `GET /readyz`: Probed for gateway readiness and individual backend states.
   - `GET /v1/models`: Populates model dropdown; falls back to free-text input if 404 or empty.

4. **Backend Pinning**:
   - Strict pinning with `X-InferMesh-Pin-Backend: <name>` (HTTP) or `?backend=<name>` (WebSocket).
   - Detects and warns if served backend differs from pinned backend (`"requested X, served by Y"`).

---

## Tests

- `npm test` (or `make frontend-test` from the repo root: type check + unit tests): the pure logic described below.
- `make frontend-e2e` (optional): a real-browser end-to-end test against the real gateway; see `e2e/README.md`.

### Unit tests

`npm test` runs the pure logic: the SSE stream parser (split chunks, role-only chunks, usage chunks, in-band errors, multi-line
breaks), the request payload builder (system prompt first with role `system`, multi-line prompt preserved, no duplicate system
message, failed/aborted turns not replayed), the Request Log export (exactly the displayed rows) and the API URL resolution.

## Notes on the Request Log

- One row per request, including failed and aborted ones, with the gateway's request ID and the elapsed time whenever they are known.
- The timestamp is when the request started. Latency, TTFT and tokens/s are measured in the browser; tokens/s uses server-reported
  usage and shows `n/a` when usage is missing (it is never estimated).
- **Export JSON** downloads exactly the rows currently displayed (after the search and status filters). It is disabled when the
  filters leave no rows, and the file includes each row's system prompt and an ISO timestamp.
