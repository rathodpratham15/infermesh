import { describe, it, expect } from 'vitest';
import { SSEStreamParser, parseSSEChunk, parseMessageBlock } from '../sse.ts';

describe('SSEStreamParser and pure SSE parsing functions', () => {
  it('handles role-only chunk followed by content chunks', () => {
    const parser = new SSEStreamParser();

    // First chunk has choices[0].delta.role only (NOT a content token)
    const roleChunk = 'data: {"choices":[{"delta":{"role":"assistant"}}]}\n\n';
    const events1 = parser.feed(roleChunk);
    expect(events1).toEqual([{ type: 'role', role: 'assistant' }]);

    // Content chunk
    const contentChunk = 'data: {"choices":[{"delta":{"content":"Hello world"}},{"delta":{}}]}\n\n';
    const events2 = parser.feed(contentChunk);
    expect(events2).toEqual([{ type: 'token', text: 'Hello world' }]);
  });

  it('handles split chunks across multiple reads', () => {
    const parser = new SSEStreamParser();

    // Partial chunk 1: "data: {\"cho"
    const e1 = parser.feed('data: {"cho');
    expect(e1).toEqual([]);

    // Partial chunk 2: "ices\":[{\"delta\":{\"content\":\"Split \"}}]}"
    const e2 = parser.feed('ices":[{"delta":{"content":"Split "}}]}');
    expect(e2).toEqual([]);

    // Final delimiter newline split across reads: "\n" then "\n"
    const e3 = parser.feed('\n');
    expect(e3).toEqual([]);
    const e4 = parser.feed('\n');
    expect(e4).toEqual([{ type: 'token', text: 'Split ' }]);

    // Next chunk split across data line
    const e5 = parser.feed('data: {"choices":[{"delta":{"content":"text"}}]}\n\n');
    expect(e5).toEqual([{ type: 'token', text: 'text' }]);
  });

  it('handles usage chunk where choices is empty', () => {
    const parser = new SSEStreamParser();

    const usageChunk =
      'data: {"choices":[],"usage":{"prompt_tokens":12,"completion_tokens":45,"total_tokens":57}}\n\n';
    const events = parser.feed(usageChunk);

    expect(events).toEqual([
      {
        type: 'usage',
        usage: { prompt_tokens: 12, completion_tokens: 45, total_tokens: 57 },
      },
    ]);
  });

  it('handles finish reason and [DONE] termination chunk', () => {
    const parser = new SSEStreamParser();

    const finishChunk = 'data: {"choices":[{"delta":{"finish_reason":"stop"}}]}\n\n';
    const eventsFinish = parser.feed(finishChunk);
    expect(eventsFinish).toEqual([{ type: 'finish', finish_reason: 'stop' }]);

    const doneChunk = 'data: [DONE]\n\n';
    const eventsDone = parser.feed(doneChunk);
    expect(eventsDone).toEqual([{ type: 'done' }]);
  });

  it('handles in-band error mid-stream', () => {
    const parser = new SSEStreamParser();

    const tokenChunk = 'data: {"choices":[{"delta":{"content":"Generating..."}}]}\n\n';
    parser.feed(tokenChunk);

    const errorChunk =
      'data: {"error":{"message":"backend connection reset","type":"backend_error","code":502}}\n\n';
    const eventsError = parser.feed(errorChunk);

    expect(eventsError).toEqual([
      {
        type: 'error',
        error: {
          message: 'backend connection reset',
          type: 'backend_error',
          code: 502,
        },
      },
    ]);
  });

  it('handles CRLF line breaks and multi-line splits', () => {
    const parser = new SSEStreamParser();

    const crlfChunk =
      'data: {"choices":[{"delta":{"content":"Line 1\\nLine 2"}}]}\r\n\r\ndata: [DONE]\r\n\r\n';
    const events = parser.feed(crlfChunk);

    expect(events).toEqual([
      { type: 'token', text: 'Line 1\nLine 2' },
      { type: 'done' },
    ]);
  });

  it('flush parses remaining complete message block if trailing newlines are omitted', () => {
    const parser = new SSEStreamParser();
    parser.feed('data: [DONE]');
    const flushed = parser.flush();
    expect(flushed).toEqual([{ type: 'done' }]);
  });
});
