// Recording OpenAI-compatible engine for the UI end-to-end tests. It is a TEST DOUBLE, not a model: it records
// what the gateway sends it and replies with canned text, so the test can check what actually went over the wire.
// Special prompts (latest message only): __FAIL__ -> HTTP 500; __SLOW__ -> slow stream (for the Stop button).
import http from 'node:http';
const captured = [];
const sleep = (ms) => new Promise((r) => setTimeout(r, ms));
http.createServer(async (req, res) => {
  if (req.url === '/v1/models') { res.setHeader('content-type', 'application/json'); return res.end('{"data":[{"id":"x"}]}'); }
  if (req.url === '/_captured') { res.setHeader('content-type', 'application/json'); return res.end(JSON.stringify(captured)); }
  if (req.url === '/_reset' && req.method === 'POST') { captured.length = 0; return res.end('ok'); }
  if (req.url === '/v1/chat/completions' && req.method === 'POST') {
    let raw = ''; for await (const c of req) raw += c;
    const body = JSON.parse(raw);
    captured.push({ requestId: req.headers['x-request-id'] || null, body });
    const text = body.messages.at(-1).content; // markers only count in the latest message, not replayed history
    if (text.includes('__FAIL__')) { res.statusCode = 500; res.setHeader('content-type', 'application/json'); return res.end('{"error":"boom"}'); }
    const slow = text.includes('__SLOW__');
    const toks = slow ? Array.from({ length: 25 }, (_, i) => `slowtok${i} `) : ['Arr', ' matey', '!'];
    const prompt = body.messages.reduce((n, m) => n + m.content.split(/\s+/).length, 0);
    if (!body.stream) {
      res.setHeader('content-type', 'application/json');
      return res.end(JSON.stringify({ choices: [{ finish_reason: 'stop', message: { role: 'assistant', content: toks.join('') } }], usage: { prompt_tokens: prompt, completion_tokens: toks.length, total_tokens: prompt + toks.length } }));
    }
    res.setHeader('content-type', 'text/event-stream');
    const send = (o) => res.write(`data: ${JSON.stringify(o)}\n\n`);
    send({ choices: [{ delta: { role: 'assistant' } }] });
    for (const t of toks) { await sleep(slow ? 400 : 40); if (res.destroyed) return; send({ choices: [{ delta: { content: t } }] }); }
    send({ choices: [{ delta: {}, finish_reason: 'stop' }] });
    send({ choices: [], usage: { prompt_tokens: prompt, completion_tokens: toks.length, total_tokens: prompt + toks.length } });
    res.write('data: [DONE]\n\n'); return res.end();
  }
  res.statusCode = 404; res.end();
}).listen(Number(process.env.ENGINE_PORT || 9101), '127.0.0.1', () => console.log('recording engine on ' + (process.env.ENGINE_PORT || 9101)));
