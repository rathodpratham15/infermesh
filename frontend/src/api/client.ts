import {
  BackendsResponse,
  CompletionRequest,
  ModelsResponse,
  ReadyzResponse,
  RequestMetrics,
  TokenUsage,
  TransportType,
} from './types.ts';
import { SSEStreamParser } from './sse.ts';
import { executeWebSocketInference } from './ws.ts';
import {
  executeMockInference,
  getMockBackends,
  getMockModels,
  getMockReadyz,
} from './mock.ts';

const DEV_DEFAULT_GATEWAY = (import.meta.env.VITE_GATEWAY_URL ?? 'http://localhost:8080') as string;

/**
 * Where an HTTP API call goes. In `vite dev` with the default gateway URL, same-origin paths are
 * used so the Vite proxy forwards them (no CORS needed). Otherwise the call goes to the
 * configured gateway URL, which must then allow this origin with CORS_ALLOWED_ORIGINS. An empty
 * gateway URL means "same origin" (for example when a reverse proxy serves UI and API together).
 */
export function resolveApiUrl(gatewayUrl: string, path: string, dev: boolean, devDefault: string): string {
  const base = (gatewayUrl || '').trim().replace(/\/+$/, '');
  if (!base) return path;
  if (dev && base === devDefault.trim().replace(/\/+$/, '')) return path;
  return base + path;
}

function apiUrl(gatewayUrl: string, path: string): string {
  return resolveApiUrl(gatewayUrl, path, import.meta.env.DEV, DEV_DEFAULT_GATEWAY);
}

export interface InferenceClientOptions {
  gatewayUrl: string;
  isMock: boolean;
  transport: TransportType;
  request: CompletionRequest;
  pinnedBackend?: string;
  forceMockError?: 'none' | '429' | '503' | 'midstream' | '400_pin';
  onStart?: (info: { requestId: string; backend: string }) => void;
  onToken?: (text: string) => void;
  onDone?: (metrics: RequestMetrics) => void;
  /** `info` carries the request ID (and backend) when the gateway reported them even though the request failed. */
  onError?: (
    errorDetail: string,
    code?: number,
    retryAfter?: string | null,
    info?: { requestId?: string; backend?: string }
  ) => void;
  onClose?: () => void;
}

export interface ClientExecutionHandle {
  abort: () => void;
}

/**
 * Fetch list of backends from /v1/backends.
 */
export async function fetchBackends(gatewayUrl: string, isMock: boolean): Promise<BackendsResponse> {
  if (isMock) {
    return getMockBackends();
  }

  const res = await fetch(apiUrl(gatewayUrl, '/v1/backends'), {
    method: 'GET',
    headers: { Accept: 'application/json' },
  });

  if (!res.ok) {
    throw new Error(`Failed to fetch /v1/backends: HTTP ${res.status} ${res.statusText}`);
  }
  return res.json();
}

/**
 * Fetch gateway health readiness from /readyz.
 */
export async function fetchReadyz(gatewayUrl: string, isMock: boolean): Promise<ReadyzResponse> {
  if (isMock) {
    return getMockReadyz();
  }

  const res = await fetch(apiUrl(gatewayUrl, '/readyz'), {
    method: 'GET',
    headers: { Accept: 'application/json' },
  });

  if (!res.ok) {
    return {
      ready: false,
      backends: { gateway: `unhealthy: HTTP ${res.status}` },
    };
  }
  return res.json();
}

/**
 * Fetch available models from /v1/models.
 * Gracefully handles 404 or empty list.
 */
export async function fetchModels(gatewayUrl: string, isMock: boolean): Promise<ModelsResponse> {
  if (isMock) {
    return getMockModels();
  }

  try {
    const res = await fetch(apiUrl(gatewayUrl, '/v1/models'), {
      method: 'GET',
      headers: { Accept: 'application/json' },
    });

    if (res.status === 404) {
      return { object: 'list', data: [] };
    }
    if (!res.ok) {
      console.warn(`GET /v1/models returned ${res.status}`);
      return { object: 'list', data: [] };
    }
    return await res.json();
  } catch (err) {
    console.warn('Error fetching /v1/models:', err);
    return { object: 'list', data: [] };
  }
}

/**
 * Unified inference execution across SSE, WebSocket, Non-streaming, or Mock.
 */
export function executeInference(options: InferenceClientOptions): ClientExecutionHandle {
  const {
    gatewayUrl,
    isMock,
    transport,
    request,
    pinnedBackend,
    forceMockError,
    onStart,
    onToken,
    onDone,
    onError,
    onClose,
  } = options;

  if (isMock) {
    return executeMockInference({
      request,
      transport,
      pinnedBackend,
      forceErrorType: forceMockError,
      onStart,
      onToken,
      onDone,
      onError: (err, code, retryAfter) => {
        onError?.(err, code, retryAfter);
        onClose?.();
      },
    });
  }

  if (transport === 'ws') {
    return executeWebSocketInference({
      gatewayUrl,
      request,
      pinnedBackend,
      onStart,
      onToken,
      onDone: (frame, timings) => {
        const completionTokens = frame.usage?.completion_tokens ?? null;
        const promptTokens = frame.usage?.prompt_tokens ?? null;
        const totalTokens = frame.usage?.total_tokens ?? null;
        const usageReported = completionTokens !== null;

        let tokensPerSec: number | null = null;
        let tokensPerSecReason: string | undefined;

        if (usageReported && timings.browserTtftMs !== null) {
          const streamingDurationSec = (timings.totalLatencyMs - timings.browserTtftMs) / 1000;
          if (streamingDurationSec > 0 && completionTokens !== null) {
            tokensPerSec = Number((completionTokens / streamingDurationSec).toFixed(1));
          }
        } else if (!usageReported) {
          tokensPerSecReason = 'server did not report token usage';
        }

        const pinningMismatchWarning =
          pinnedBackend && pinnedBackend !== frame.backend
            ? `requested ${pinnedBackend}, served by ${frame.backend}`
            : undefined;

        onDone?.({
          requestId: frame.request_id || 'n/a',
          backendServed: frame.backend || 'unknown',
          pinnedBackend,
          pinningMismatchWarning,
          totalLatencyMs: timings.totalLatencyMs,
          ttftMs: timings.browserTtftMs,
          serverTtftMs: timings.serverTtftMs,
          tokensPerSec,
          tokensPerSecReason,
          completionTokens,
          promptTokens,
          totalTokens,
          finishReason: frame.finish_reason || 'stop',
          usageReported,
          timestamp: Date.now(),
          transport: 'ws',
          model: request.model,
          status: 'success',
          isMock: false,
        });
      },
      onError: (errMsg, code, requestId) =>
        onError?.(errMsg, code, null, requestId ? { requestId } : undefined),
      onClose,
    });
  }

  // HTTP transports (SSE or Non-streaming)
  const abortController = new AbortController();
  const startTime = performance.now();

  const headers: Record<string, string> = {
    'Content-Type': 'application/json',
  };

  if (pinnedBackend && pinnedBackend.trim()) {
    headers['X-InferMesh-Pin-Backend'] = pinnedBackend.trim();
  }

  const endpoint = apiUrl(gatewayUrl, '/v1/chat/completions');
  const isStreaming = transport === 'sse';

  (async () => {
    try {
      const response = await fetch(endpoint, {
        method: 'POST',
        headers,
        body: JSON.stringify({
          ...request,
          stream: isStreaming,
        }),
        signal: abortController.signal,
      });

      const requestId = response.headers.get('X-Request-ID') || 'n/a';
      const backendServed = response.headers.get('X-InferMesh-Backend') || 'unknown';
      const retryAfter = response.headers.get('Retry-After');

      const pinningMismatchWarning =
        pinnedBackend && backendServed !== 'unknown' && pinnedBackend !== backendServed
          ? `requested ${pinnedBackend}, served by ${backendServed}`
          : undefined;

      // Handle HTTP error responses
      if (!response.ok) {
        let errorMsg = `HTTP ${response.status} ${response.statusText}`;
        try {
          const errBody = await response.json();
          if (errBody?.error?.message) {
            errorMsg = errBody.error.message;
            if (errBody.error.type) {
              errorMsg += ` (${errBody.error.type})`;
            }
          }
        } catch {
          // Response body was not JSON
        }

        if (response.status === 429) {
          const waitStr = retryAfter ? ` Retry-After: ${retryAfter}s.` : '';
          errorMsg = `Rate limit exceeded (429).${waitStr} ${errorMsg}`;
        } else if (response.status === 403) {
          errorMsg = `Backend pinning is disabled on the gateway (403): ${errorMsg}`;
        } else if (response.status === 503) {
          errorMsg = `Backend unavailable (503): ${errorMsg}`;
        } else if (response.status === 502 || response.status === 504) {
          errorMsg = `Gateway backend error (${response.status}): ${errorMsg}`;
        }

        onError?.(errorMsg, response.status, retryAfter, {
          requestId: requestId !== 'n/a' ? requestId : undefined,
          backend: backendServed !== 'unknown' ? backendServed : undefined,
        });
        onClose?.();
        return;
      }

      // Notify start with headers
      onStart?.({ requestId, backend: backendServed });

      if (!isStreaming) {
        // Non-streaming HTTP
        const data = await response.json();
        const totalLatencyMs = Math.max(1, Math.round(performance.now() - startTime));
        const choice = data.choices?.[0];
        const content = choice?.message?.content || '';
        const finishReason = choice?.finish_reason || 'stop';

        onToken?.(content);

        const usage: TokenUsage = data.usage || {};
        const completionTokens = typeof usage.completion_tokens === 'number' ? usage.completion_tokens : null;
        const promptTokens = typeof usage.prompt_tokens === 'number' ? usage.prompt_tokens : null;
        const totalTokens = typeof usage.total_tokens === 'number' ? usage.total_tokens : null;
        const usageReported = completionTokens !== null;

        let tokensPerSec: number | null = null;
        let tokensPerSecReason: string | undefined;

        if (usageReported && completionTokens !== null) {
          tokensPerSec = Number((completionTokens / (totalLatencyMs / 1000)).toFixed(1));
        } else {
          tokensPerSecReason = 'server did not report token usage';
        }

        onDone?.({
          requestId,
          backendServed,
          pinnedBackend,
          pinningMismatchWarning,
          totalLatencyMs,
          ttftMs: null, // Not available for non-streaming
          serverTtftMs: null,
          tokensPerSec,
          tokensPerSecReason,
          completionTokens,
          promptTokens,
          totalTokens,
          finishReason,
          usageReported,
          timestamp: Date.now(),
          transport: 'non-streaming',
          model: request.model,
          status: 'success',
          isMock: false,
        });

        onClose?.();
        return;
      }

      // SSE Streaming via fetch() + ReadableStream
      if (!response.body) {
        throw new Error('Response body is null');
      }

      const reader = response.body.getReader();
      const decoder = new TextDecoder('utf-8');
      const parser = new SSEStreamParser();

      let browserTtftMs: number | null = null;
      let streamFinishReason = 'stop';
      let streamUsage: TokenUsage = {};
      let streamTerminatedWithDone = false;

      while (true) {
        const { done, value } = await reader.read();
        if (done) break;

        const textChunk = decoder.decode(value, { stream: true });
        const events = parser.feed(textChunk);

        for (const evt of events) {
          if (evt.type === 'token') {
            if (browserTtftMs === null) {
              browserTtftMs = Math.max(1, Math.round(performance.now() - startTime));
            }
            onToken?.(evt.text);
          } else if (evt.type === 'finish') {
            streamFinishReason = evt.finish_reason;
          } else if (evt.type === 'usage') {
            streamUsage = { ...streamUsage, ...evt.usage };
          } else if (evt.type === 'done') {
            streamTerminatedWithDone = true;
          } else if (evt.type === 'error') {
            onError?.(
              `In-band stream error: ${evt.error.message} (${evt.error.type || 'error'})`,
              typeof evt.error.code === 'number' ? evt.error.code : 500,
              null
            );
            onClose?.();
            return;
          }
        }
      }

      // Flush remaining data if any
      const remainingEvents = parser.flush();
      for (const evt of remainingEvents) {
        if (evt.type === 'token') {
          if (browserTtftMs === null) {
            browserTtftMs = Math.max(1, Math.round(performance.now() - startTime));
          }
          onToken?.(evt.text);
        } else if (evt.type === 'finish') {
          streamFinishReason = evt.finish_reason;
        } else if (evt.type === 'usage') {
          streamUsage = { ...streamUsage, ...evt.usage };
        } else if (evt.type === 'done') {
          streamTerminatedWithDone = true;
        } else if (evt.type === 'error') {
          onError?.(
            `In-band stream error: ${evt.error.message}`,
            typeof evt.error.code === 'number' ? evt.error.code : 500,
            null
          );
          onClose?.();
          return;
        }
      }

      const totalLatencyMs = Math.max(1, Math.round(performance.now() - startTime));
      const completionTokens =
        typeof streamUsage.completion_tokens === 'number' ? streamUsage.completion_tokens : null;
      const promptTokens =
        typeof streamUsage.prompt_tokens === 'number' ? streamUsage.prompt_tokens : null;
      const totalTokens =
        typeof streamUsage.total_tokens === 'number' ? streamUsage.total_tokens : null;
      const usageReported = completionTokens !== null;

      let tokensPerSec: number | null = null;
      let tokensPerSecReason: string | undefined;

      if (usageReported && browserTtftMs !== null && completionTokens !== null) {
        const streamingDurationSec = (totalLatencyMs - browserTtftMs) / 1000;
        if (streamingDurationSec > 0) {
          tokensPerSec = Number((completionTokens / streamingDurationSec).toFixed(1));
        }
      } else if (!usageReported) {
        tokensPerSecReason = 'server did not report token usage';
      }

      onDone?.({
        requestId,
        backendServed,
        pinnedBackend,
        pinningMismatchWarning,
        totalLatencyMs,
        ttftMs: browserTtftMs,
        serverTtftMs: null,
        tokensPerSec,
        tokensPerSecReason,
        completionTokens,
        promptTokens,
        totalTokens,
        finishReason: streamFinishReason,
        usageReported,
        timestamp: Date.now(),
        transport: 'sse',
        model: request.model,
        status: streamTerminatedWithDone ? 'success' : 'aborted',
        isMock: false,
      });

      onClose?.();
    } catch (err: unknown) {
      if (abortController.signal.aborted) {
        onClose?.();
        return;
      }
      const errMessage =
        err instanceof Error ? err.message : 'Gateway unreachable or network connection failed';
      onError?.(errMessage, 0, null);
      onClose?.();
    }
  })();

  return {
    abort: () => {
      abortController.abort();
    },
  };
}
