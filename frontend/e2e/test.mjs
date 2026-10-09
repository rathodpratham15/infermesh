// End-to-end test of the InferMesh console in a real browser (Chromium) against the REAL gateway and a recording
// engine. Run it with `make frontend-e2e` (see e2e/README.md).
import { chromium } from 'playwright';
import fs from 'node:fs';
const UI = process.env.UI_URL || 'http://localhost:5173';
const ENGINE = `http://127.0.0.1:${process.env.ENGINE_PORT || 9101}`;
const OUT = process.argv[2] || '.';
const captured = async () => (await fetch(ENGINE + '/_captured')).json();
let pass = 0, fail = 0;
const check = (name, ok, detail = '') => { (ok ? pass++ : fail++); console.log(`${ok ? 'PASS' : 'FAIL'}  ${name}${ok ? '' : '   <-- ' + detail}`); };
const SYSTEM = 'You are a pirate.\n\nAlways say arr.\n  Line four, indented.';

const browser = await chromium.launch();
const ctx = await browser.newContext({ viewport: { width: 1500, height: 950 }, acceptDownloads: true });
const page = await ctx.newPage();
const consoleErrors = [];
// The browser itself logs the deliberately provoked 502 (the __FAIL__ step); that is not an app error.
page.on('console', (m) => { if (m.type() === 'error' && !/status of 502/.test(m.text())) consoleErrors.push(m.text()); });
page.on('pageerror', (e) => consoleErrors.push('pageerror: ' + e.message));

await page.goto(UI);
await page.getByText('Test Console').first().waitFor();
check('real mode (no MOCK banner)', (await page.getByText(/MOCK MODE/i).count()) === 0);

const sys = page.getByPlaceholder(/System instructions/i);
const input = page.getByPlaceholder(/Send inference prompt/i);
check('system prompt is a multi-line <textarea>', (await sys.evaluate((el) => el.tagName)) === 'TEXTAREA');
await sys.fill(SYSTEM);
await page.waitForTimeout(300);
const modelSel = page.locator('select').filter({ has: page.locator('option[value="model-a"]') }).first();
check('model selector populated from /v1/models and a real model auto-selected', (await modelSel.inputValue()) === 'model-a', await modelSel.inputValue().catch(() => 'none'));

const send = async (text, waitText) => {
  await input.fill(text); await input.press('Enter');
  await page.getByText(waitText ?? /Arr matey!/).last().waitFor({ timeout: 15000 });
  await page.waitForTimeout(250);
};

// ---- 1. SSE
await page.getByRole('button', { name: 'SSE', exact: true }).click();
await send('hello sse');
let c = await captured();
let b = c[0].body;
check('SSE: messages[0] is the system prompt, role "system", multi-line intact', b.messages[0].role === 'system' && b.messages[0].content === SYSTEM, JSON.stringify(b.messages[0]));
check('SSE: user prompt last, stream=true', b.messages.at(-1).content === 'hello sse' && b.stream === true);
check('SSE: exactly one system message', b.messages.filter((m) => m.role === 'system').length === 1);

// ---- 2. WebSocket
await page.getByRole('button', { name: 'WebSocket', exact: true }).click();
await send('second via ws');
c = await captured(); b = c[1].body;
check('WS: messages[0] is the system prompt', b.messages[0].role === 'system' && b.messages[0].content === SYSTEM, JSON.stringify(b.messages[0]));
check('WS: history replayed after the system prompt (user, assistant, user)', JSON.stringify(b.messages.slice(1).map((m) => m.role)) === '["user","assistant","user"]', JSON.stringify(b.messages.map((m) => m.role)));
check('WS: still exactly one system message', b.messages.filter((m) => m.role === 'system').length === 1);

// ---- 3. Non-streaming
await page.getByRole('button', { name: 'Non-stream', exact: true }).click();
await send('third non-stream');
c = await captured(); b = c[2].body;
check('Non-streaming: messages[0] is the system prompt, stream=false', b.messages[0].role === 'system' && b.messages[0].content === SYSTEM && !b.stream, JSON.stringify({ s: b.stream, m0: b.messages[0] }));

// ---- 4. Pin to backend b (SSE)
await page.getByRole('button', { name: 'SSE', exact: true }).click();
const backendSel = page.locator('select').filter({ has: page.locator('option', { hasText: 'Auto (router decides)' }) }).first();
await backendSel.selectOption('b');
await send('pinned to b');
c = await captured();
check('pin to backend b reaches backend b (its configured model is model-b)', c[3].body.model === 'model-b', c[3].body.model);
await backendSel.selectOption('');

// ---- 5. Failure -> logged with real details
await input.fill('please __FAIL__'); await input.press('Enter');
await page.getByText('Request Failed').first().waitFor({ timeout: 15000 });
await page.waitForTimeout(300);

// ---- 6. Abort mid-stream
await input.fill('please __SLOW__'); await input.press('Enter');
await page.getByText(/slowtok1/).first().waitFor({ timeout: 15000 });
await page.getByRole('button', { name: /stop/i }).first().click();
await page.waitForTimeout(500);
await page.screenshot({ path: `${OUT}/console.png` });
await send('after abort', /Arr matey!/);
c = await captured(); b = c.at(-1).body;
const flat = JSON.stringify(b.messages);
check('aborted partial reply is NOT replayed to the model', !flat.includes('slowtok') && !flat.includes('[aborted') && !flat.includes('aborted by user'), flat.slice(0, 200));

// ---- 7. Request Log tab
await page.getByRole('button', { name: /Request Log/ }).click();
await page.locator('table').waitFor();
const heads = (await page.locator('thead th').allInnerTexts()).map((s) => s.trim().toLowerCase()).filter(Boolean);
const want = ['timestamp', 'transport', 'backend', 'status / error', 'latency', 'ttft', 'tokens/s', 'output', 'request id'];
check('table has all requested columns', want.every((w) => heads.includes(w)), JSON.stringify(heads));
const rows = page.locator('tbody tr');
const nRows = await rows.count();
check('one row per request (3 transports + pin + failure + abort + follow-up = 7)', nRows === 7, `rows=${nRows}`);
const rowData = [];
for (let i = 0; i < nRows; i++) {
  const cells = (await rows.nth(i).locator('td').allInnerTexts()).map((s) => s.trim());
  rowData.push(cells);
}
console.log('rows (newest first):'); rowData.forEach((r) => console.log('   ', JSON.stringify(r.slice(1))));
const engineIds = new Set(c.map((x) => x.requestId).filter(Boolean));
const okRows = rowData.filter((r) => /200 OK/.test(r[4]));
check('every successful row shows the REAL gateway request ID (matches what the engine received)', okRows.length === 5 && okRows.every((r) => [...engineIds].some((id) => r[9].includes(id.slice(0, 12)))), JSON.stringify(okRows.map((r) => r[9])));
const failRow = rowData.find((r) => !/200 OK|Aborted/.test(r[4]));
const abortRow = rowData.find((r) => /Aborted/.test(r[4]));
check('failed row: shows the error, a real request ID and a non-zero latency', failRow && failRow[9] !== 'n/a' && !/^0ms$/.test(failRow[5]) && /50[24]|backend/i.test(failRow[4]), JSON.stringify(failRow));
check('aborted row: real request ID/backend and non-zero latency', abortRow && abortRow[9] !== 'n/a' && !/unknown|unassigned/.test(abortRow[3]) && !/^0ms$/.test(abortRow[5]), JSON.stringify(abortRow));
check('transports shown: sse, ws, non-streaming', ['sse', 'ws', 'non-streaming'].every((t) => rowData.some((r) => r[2].toLowerCase() === t)));
check('pinned row shows backend b', rowData.some((r) => r[3].startsWith('b')));
await page.screenshot({ path: `${OUT}/log.png` });

// ---- 8. Export JSON
const exportBtn = page.getByRole('button', { name: /Export JSON/ });
const dl = async () => { const [d] = await Promise.all([page.waitForEvent('download', { timeout: 5000 }), exportBtn.click()]); const p = await d.path(); return { name: d.suggestedFilename(), json: JSON.parse(fs.readFileSync(p, 'utf8')) }; };
let exp = await dl();
check('export: downloads a .json file named infermesh-request-log-*', /^infermesh-request-log-.*\.json$/.test(exp.name), exp.name);
check('export: contains all 7 displayed rows', exp.json.length === 7, String(exp.json.length));
const need = ['timestamp', 'isoTimestamp', 'transport', 'backendServed', 'status', 'totalLatencyMs', 'ttftMs', 'tokensPerSec', 'completionTokens', 'requestId'];
check('export: every row carries the table fields', exp.json.every((r) => need.every((k) => k in r)), JSON.stringify(Object.keys(exp.json[0])));
check('export: same order as the table (newest first)', exp.json[0].requestId.startsWith(rowData[0][9].replace('…', '').slice(0, 8)) || rowData[0][9].includes(exp.json[0].requestId.slice(0, 8)));
check('export: system prompt recorded with the rows', exp.json.filter((r) => r.systemPrompt === SYSTEM).length === 7);
await page.getByRole('button', { name: 'error', exact: true }).click();
exp = await dl();
check('export respects the filter: status=error exports only the error row(s)', exp.json.length === 1 && exp.json.every((r) => r.status === 'error'), String(exp.json.length));
await page.getByRole('button', { name: 'all', exact: true }).click();
await page.getByPlaceholder(/Search by Request ID/i).fill('zzz-matches-nothing');
await page.waitForTimeout(200);
check('export button is disabled when the filter shows no rows (never exports the hidden log)', await exportBtn.isDisabled());
await page.getByPlaceholder(/Search by Request ID/i).fill('');

check('no browser console errors', consoleErrors.length === 0, consoleErrors.slice(0, 3).join(' | '));
await browser.close();
console.log(`\n${pass} passed, ${fail} failed`);
process.exit(fail ? 1 : 0);
