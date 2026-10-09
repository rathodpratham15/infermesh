import { describe, expect, it } from 'vitest';
import { buildLogExport, exportFileName } from '../../utils/logExport.ts';
import { RequestLogEntry } from '../types.ts';

const entry = (id: string, over: Partial<RequestLogEntry> = {}): RequestLogEntry => ({
  id, requestId: 'req-' + id, backendServed: 'a', totalLatencyMs: 100, ttftMs: 10, tokensPerSec: 50,
  completionTokens: 5, promptTokens: 3, totalTokens: 8, finishReason: 'stop', usageReported: true,
  timestamp: Date.UTC(2026, 9, 9, 12, 0, 0), transport: 'sse', model: 'm', status: 'success',
  isMock: false, promptPreview: 'p', responsePreview: '', ...over,
});

describe('Request log export', () => {
  it('exports exactly the displayed rows, in order, with an ISO timestamp', () => {
    const displayed = [entry('2'), entry('1')];
    const out = buildLogExport(displayed);
    expect(out.map((r) => r.id)).toEqual(['2', '1']);
    expect(out[0].isoTimestamp).toBe('2026-10-09T12:00:00.000Z');
    // all the columns the table shows are present in the export
    for (const k of ['timestamp', 'transport', 'backendServed', 'status', 'totalLatencyMs', 'ttftMs', 'tokensPerSec', 'completionTokens', 'requestId']) {
      expect(out[0]).toHaveProperty(k);
    }
  });

  it('exports an empty array when the filters leave nothing on screen (never the unfiltered log)', () => {
    expect(buildLogExport([])).toEqual([]);
  });

  it('keeps failed and aborted rows and the error text', () => {
    const out = buildLogExport([entry('e', { status: 'error', errorDetail: 'HTTP 503', tokensPerSec: null, completionTokens: null })]);
    expect(out[0]).toMatchObject({ status: 'error', errorDetail: 'HTTP 503', tokensPerSec: null });
  });

  it('does not throw on an invalid timestamp', () => {
    expect(buildLogExport([entry('x', { timestamp: NaN })])[0].isoTimestamp).toBeNull();
  });

  it('builds a filesystem-safe file name', () => {
    expect(exportFileName(new Date('2026-10-09T12:34:56.789Z'))).toBe('infermesh-request-log-2026-10-09T12-34-56-789Z.json');
  });
});
