import { SSEEvent, TokenUsage } from './types.ts';

/**
 * Pure function that parses an incoming chunk of SSE text given the current buffer.
 * Returns the parsed SSE events and the remaining unparsed buffer.
 *
 * Server-Sent Events protocol:
 * - Messages are separated by two newlines (`\n\n` or `\r\n\r\n`).
 * - Lines start with "data: " (or "data:").
 * - Special terminal line: "data: [DONE]"
 * - In-band errors: "data: {"error":{"message":...}}"
 * - Role-only chunk: choices[0].delta.role without content
 * - Content chunks: choices[0].delta.content
 * - Finish chunks: choices[0].delta.finish_reason or choices[0].finish_reason
 * - Usage chunks: choices: [] with usage, or chunk containing usage
 */
export function parseSSEChunk(
  currentBuffer: string,
  newChunk: string
): { events: SSEEvent[]; remainingBuffer: string } {
  const combined = currentBuffer + newChunk;
  const events: SSEEvent[] = [];

  // Match event boundaries (double newlines, handling both CRLF and LF)
  let searchIndex = 0;

  while (searchIndex < combined.length) {
    // Look for double newline indicating the end of an SSE message block
    const lfMatch = combined.indexOf('\n\n', searchIndex);
    const crlfMatch = combined.indexOf('\r\n\r\n', searchIndex);

    let delimiterPos = -1;
    let delimiterLen = 0;

    if (lfMatch !== -1 && crlfMatch !== -1) {
      if (lfMatch < crlfMatch) {
        delimiterPos = lfMatch;
        delimiterLen = 2;
      } else {
        delimiterPos = crlfMatch;
        delimiterLen = 4;
      }
    } else if (lfMatch !== -1) {
      delimiterPos = lfMatch;
      delimiterLen = 2;
    } else if (crlfMatch !== -1) {
      delimiterPos = crlfMatch;
      delimiterLen = 4;
    }

    if (delimiterPos === -1) {
      // No complete event delimiter found; the rest remains in buffer
      break;
    }

    const messageBlock = combined.slice(searchIndex, delimiterPos);
    searchIndex = delimiterPos + delimiterLen;

    const parsedEvents = parseMessageBlock(messageBlock);
    events.push(...parsedEvents);
  }

  const remainingBuffer = combined.slice(searchIndex);
  return { events, remainingBuffer };
}

/**
 * Parses a single SSE message block consisting of lines.
 */
export function parseMessageBlock(block: string): SSEEvent[] {
  const lines = block.split(/\r?\n/);
  const dataLines: string[] = [];

  for (const line of lines) {
    const trimmed = line.trimStart();
    if (trimmed.startsWith('data:')) {
      const dataContent = trimmed.slice(5).trim();
      dataLines.push(dataContent);
    }
  }

  if (dataLines.length === 0) {
    return [];
  }

  const dataPayload = dataLines.join('\n').trim();
  if (!dataPayload) {
    return [];
  }

  // Check for [DONE]
  if (dataPayload === '[DONE]') {
    return [{ type: 'done' }];
  }

  try {
    const parsed = JSON.parse(dataPayload);
    const events: SSEEvent[] = [];

    // In-band error format: data: {"error":{"message":..,"type":..}}
    if (parsed.error && typeof parsed.error === 'object') {
      events.push({
        type: 'error',
        error: {
          message: parsed.error.message || 'Unknown streaming error',
          type: parsed.error.type,
          code: parsed.error.code,
        },
      });
      return events;
    }

    // Choices processing
    const choices = Array.isArray(parsed.choices) ? parsed.choices : [];

    // If choices is empty and usage is present (e.g. usage-only chunk)
    if (choices.length === 0 && parsed.usage) {
      events.push({
        type: 'usage',
        usage: normalizeUsage(parsed.usage),
      });
      return events;
    }

    for (const choice of choices) {
      const delta = choice.delta;
      if (delta) {
        // First chunk has choices[0].delta.role only (NOT a content token)
        if (delta.role && delta.content === undefined) {
          events.push({ type: 'role', role: delta.role });
        }

        // Chunks with choices[0].delta.content
        if (typeof delta.content === 'string' && delta.content.length > 0) {
          events.push({ type: 'token', text: delta.content });
        }

        // Chunks with finish_reason in delta
        if (delta.finish_reason) {
          events.push({ type: 'finish', finish_reason: delta.finish_reason });
        }
      }

      // Some providers put finish_reason on choice directly
      if (choice.finish_reason && (!delta || !delta.finish_reason)) {
        events.push({ type: 'finish', finish_reason: choice.finish_reason });
      }
    }

    // Also check if usage was attached to the same chunk as choices
    if (parsed.usage) {
      events.push({
        type: 'usage',
        usage: normalizeUsage(parsed.usage),
      });
    }

    return events;
  } catch (err) {
    // If not valid JSON and not [DONE], ignore or treat as unparseable
    console.warn('Failed to parse SSE JSON payload:', dataPayload, err);
    return [];
  }
}

function normalizeUsage(raw: unknown): TokenUsage {
  if (!raw || typeof raw !== 'object') return {};
  const obj = raw as Record<string, unknown>;
  return {
    prompt_tokens: typeof obj.prompt_tokens === 'number' ? obj.prompt_tokens : undefined,
    completion_tokens: typeof obj.completion_tokens === 'number' ? obj.completion_tokens : undefined,
    total_tokens: typeof obj.total_tokens === 'number' ? obj.total_tokens : undefined,
  };
}

/**
 * Stateful SSE Parser class wrapping `parseSSEChunk` for streaming consumption.
 */
export class SSEStreamParser {
  private buffer = '';

  /**
   * Feed raw text chunk into the parser, returning any newly parsed events.
   */
  public feed(chunk: string): SSEEvent[] {
    const { events, remainingBuffer } = parseSSEChunk(this.buffer, chunk);
    this.buffer = remainingBuffer;
    return events;
  }

  /**
   * Flush any remaining data at the end of the stream.
   */
  public flush(): SSEEvent[] {
    if (!this.buffer.trim()) {
      this.buffer = '';
      return [];
    }
    const events = parseMessageBlock(this.buffer);
    this.buffer = '';
    return events;
  }

  /**
   * Reset parser internal buffer.
   */
  public reset(): void {
    this.buffer = '';
  }
}
