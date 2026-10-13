# Browser end-to-end test

An opt-in test that drives the console in a real browser (headless Chromium via Playwright) against the **real
gateway binary**, with a small **recording engine** behind it. The engine is a test double, not a model: it saves
every request body the gateway sends upstream, so the test checks what actually went over the wire, and it can be
told to fail or to stream slowly (for the Stop button).

```bash
cd frontend/e2e
npm install
npx playwright install chromium     # one time (~100 MB)
npm test                            # or: make frontend-e2e from the repo root
```

Needs Go, and Node 20.19+ or 22.12+. It builds the gateway from this repo, so run it from a checkout.

It checks, in a browser:

- the system prompt is a multi-line field and is sent as `messages[0]` with role `system` for **SSE, WebSocket and
  non-streaming**, once, with the text intact, and the history follows it;
- a model is auto-selected from `/v1/models`, and pinning to a backend reaches that backend;
- a user-stopped reply is **not** replayed to the model, and the log row for it (and for a failed request) has the real request
  ID, backend and a non-zero latency;
- the Request Log table has all columns, one row per request, request IDs that match what the engine received;
- Export JSON downloads exactly the displayed rows, respects the status filter, and is disabled when no rows are shown;
- no unexpected browser console errors.

Output (screenshots, logs) goes to `out/` (ignored by git).
