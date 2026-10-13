import { describe, expect, it } from 'vitest';
import { buildPayloadMessages } from '../payload.ts';
import { ChatMessage } from '../types.ts';

const msg = (role: ChatMessage['role'], content: string, extra: Partial<ChatMessage> = {}): ChatMessage => ({
  id: Math.random().toString(36), role, content, timestamp: 0, ...extra,
});

describe('buildPayloadMessages (system prompt handling)', () => {
  it('puts a non-empty system prompt first with role "system", then the user prompt', () => {
    expect(buildPayloadMessages('Be terse.', [], 'hi')).toEqual([
      { role: 'system', content: 'Be terse.' },
      { role: 'user', content: 'hi' },
    ]);
  });

  it('keeps a multi-line system prompt intact (internal newlines and blank lines), trimming only the ends', () => {
    const prompt = '  \nYou are a helpful assistant.\n\n- Rule one\n- Rule two\n  \n';
    const out = buildPayloadMessages(prompt, [], 'q');
    expect(out[0]).toEqual({ role: 'system', content: 'You are a helpful assistant.\n\n- Rule one\n- Rule two' });
  });

  it('sends no system message when the prompt is empty or whitespace', () => {
    for (const p of ['', '   ', '\n\n']) {
      expect(buildPayloadMessages(p, [], 'hi')).toEqual([{ role: 'user', content: 'hi' }]);
    }
  });

  it('keeps the system prompt as messages[0] across a multi-turn conversation, never duplicated', () => {
    const history = [
      msg('system', 'OLD system message'), // a stale system message must be dropped
      msg('user', 'first'),
      msg('assistant', 'answer'),
    ];
    const out = buildPayloadMessages('NEW system', history, 'second');
    expect(out.map((m) => m.role)).toEqual(['system', 'user', 'assistant', 'user']);
    expect(out[0].content).toBe('NEW system');
    expect(out.filter((m) => m.role === 'system')).toHaveLength(1);
    expect(out[out.length - 1]).toEqual({ role: 'user', content: 'second' });
  });

  it('does not replay failed, aborted or empty turns to the model', () => {
    const history = [
      msg('user', 'q1'),
      msg('assistant', 'partial text', { aborted: true }),
      msg('user', 'q2'),
      msg('assistant', 'half', { error: 'boom' }),
      msg('user', 'q3'),
      msg('assistant', ''),
    ];
    const out = buildPayloadMessages('', history, 'q4');
    expect(out.map((m) => m.content)).toEqual(['q1', 'q2', 'q3', 'q4']);
    expect(JSON.stringify(out)).not.toContain('aborted');
  });
});
