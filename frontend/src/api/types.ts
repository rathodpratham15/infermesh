export type Role = 'system' | 'user' | 'assistant';

export interface ChatMessage {
  id: string;
  role: Role;
  content: string;
  timestamp: number;
  metrics?: RequestMetrics;
  error?: string;
  aborted?: boolean; // stopped by the user; not replayed to the model
  isStreaming?: boolean;
}

export interface CompletionRequest {
  model: string;
  messages: Array<{ role: string; content: string }>;
  max_tokens?: number;
  temperature?: number;
  stream?: boolean;
}

export interface BackendInfo {
  name: string;
  healthy: boolean;
  active: number;
  max_concurrent: number;
  weight: number;
  completed: number;
  failures: number;
  latency_ewma_ms: number;
}

export interface BackendsResponse {
  queue_depth: number;
  backends: BackendInfo[];
}

export interface ReadyzResponse {
  ready: boolean;
  backends: Record<string, string>;
}

export interface ModelItem {
  id: string;
  object: 'model';
  owned_by?: string;
  backend?: string;
}

export interface ModelsResponse {
  object: 'list';
  data: ModelItem[];
}

export interface TokenUsage {
  prompt_tokens?: number;
  completion_tokens?: number;
  total_tokens?: number;
}

export type TransportType = 'sse' | 'ws' | 'non-streaming';

export interface RequestMetrics {
  requestId: string;
  backendServed: string;
  pinnedBackend?: string;
  pinningMismatchWarning?: string;
  totalLatencyMs: number;
  ttftMs: number | null; // Browser-measured TTFT (start to first content token)
  serverTtftMs?: number | null; // Server-reported TTFT from WS "done" frame
  tokensPerSec: number | null; // Calculated strictly according to metrics rules
  tokensPerSecReason?: string;
  completionTokens: number | null;
  promptTokens: number | null;
  totalTokens: number | null;
  finishReason: string | null;
  usageReported: boolean;
  timestamp: number;
  transport: TransportType;
  model: string;
  systemPrompt?: string;
  status: 'success' | 'error' | 'aborted';
  errorDetail?: string;
  retryAfter?: string | null;
  isMock: boolean;
}

export interface RequestLogEntry extends RequestMetrics {
  id: string;
  promptPreview: string;
  responsePreview: string;
}

export interface ErrorResponsePayload {
  error: {
    message: string;
    type?: string;
    code?: string | number;
  };
}

export type SSEEvent =
  | { type: 'role'; role: string }
  | { type: 'token'; text: string }
  | { type: 'finish'; finish_reason: string }
  | { type: 'usage'; usage: TokenUsage }
  | { type: 'done' }
  | { type: 'error'; error: { message: string; type?: string; code?: string | number } };

export interface WSStartFrame {
  type: 'start';
  request_id: string;
  backend: string;
}

export interface WSTokenFrame {
  type: 'token';
  text: string;
}

export interface WSDoneFrame {
  type: 'done';
  request_id: string;
  backend: string;
  finish_reason?: string;
  usage?: TokenUsage;
  ttft_ms?: number;
}

export interface WSErrorFrame {
  type: 'error';
  request_id?: string;
  error: string | { message: string; type?: string };
}

export type WSServerFrame = WSStartFrame | WSTokenFrame | WSDoneFrame | WSErrorFrame;
