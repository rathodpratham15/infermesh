import { ChatMessage } from './types.ts';

export interface WireMessage {
  role: string;
  content: string;
}

/**
 * Builds the `messages` array sent to the gateway, identically for SSE, WebSocket and
 * non-streaming requests.
 *
 * - A non-empty system prompt is ALWAYS the first message, with role 'system'. Only leading and
 *   trailing whitespace is trimmed; internal newlines and blank lines are preserved.
 * - Earlier system messages in the history are dropped, so the prompt is never duplicated.
 * - Turns that failed, were aborted, or are empty are not replayed to the model.
 * - The new user prompt is last.
 */
export function buildPayloadMessages(
  systemPrompt: string,
  history: ChatMessage[],
  userPrompt: string
): WireMessage[] {
  const out: WireMessage[] = [];
  const system = systemPrompt.trim();
  if (system) {
    out.push({ role: 'system', content: system });
  }
  for (const m of history) {
    if (m.role === 'system' || m.error || m.aborted || !m.content) continue;
    out.push({ role: m.role, content: m.content });
  }
  out.push({ role: 'user', content: userPrompt });
  return out;
}
