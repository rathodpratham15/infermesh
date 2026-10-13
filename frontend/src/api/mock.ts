import {
  BackendsResponse,
  CompletionRequest,
  ModelsResponse,
  ReadyzResponse,
  RequestMetrics,
  TokenUsage,
  TransportType,
} from './types.ts';

let mockRotationCounter = 0;
let mockActiveA = 2;
let mockActiveB = 1;
let mockCompletedA = 1240;
let mockCompletedB = 1180;
let mockFailuresA = 2;
let mockFailuresB = 3;
let mockBackendBHealthy = true;

export function toggleMockBackendBHealth(healthy?: boolean) {
  mockBackendBHealthy = healthy !== undefined ? healthy : !mockBackendBHealthy;
}

export function getMockBackends(): BackendsResponse {
  // Add gentle jitter to EWMA to simulate real gateway telemetry
  const jitterA = (Math.sin(Date.now() / 5000) * 12);
  const jitterB = (Math.cos(Date.now() / 6000) * 18);

  return {
    queue_depth: Math.floor(Math.random() * 2),
    backends: [
      {
        name: 'mock-a',
        healthy: true,
        active: mockActiveA,
        max_concurrent: 16,
        weight: 100,
        completed: mockCompletedA,
        failures: mockFailuresA,
        latency_ewma_ms: Number((138.4 + jitterA).toFixed(1)),
      },
      {
        name: 'mock-b',
        healthy: mockBackendBHealthy,
        active: mockActiveB,
        max_concurrent: 16,
        weight: 100,
        completed: mockCompletedB,
        failures: mockFailuresB,
        latency_ewma_ms: Number((162.1 + jitterB).toFixed(1)),
      },
    ],
  };
}

export function getMockReadyz(): ReadyzResponse {
  return {
    ready: true,
    backends: {
      'mock-a': 'ok',
      'mock-b': mockBackendBHealthy ? 'ok' : 'unhealthy: healthcheck timeout',
    },
  };
}

export function getMockModels(): ModelsResponse {
  return {
    object: 'list',
    data: [
      {
        id: 'mock-llama-3.1-8b-instruct',
        object: 'model',
        owned_by: 'infermesh-mock',
        backend: 'mock-a',
      },
      {
        id: 'mock-mistral-7b-instruct',
        object: 'model',
        owned_by: 'infermesh-mock',
        backend: 'mock-b',
      },
      {
        id: 'mock-qwen-2.5-coder',
        object: 'model',
        owned_by: 'infermesh-mock',
      },
    ],
  };
}

const MOCK_SAMPLE_RESPONSES = [
  'Simulated response from InferMesh gateway: The load balancer efficiently dispatches tokens across active inference backends. Notice the TTFT measurement captured upon the first content chunk, followed by EWMA latency updates.',
  'InferMesh test telemetry verification: Backend routing operates on dynamic concurrency and latency EWMA weights. In this mock mode, each token chunk is streamed with randomized delay to test UI responsiveness and buffer handling.',
  'Routing benchmark sample: Verifying HTTP SSE vs WebSocket streaming protocols. The WebSocket frames stream start, token, and done packets with server-measured ttft_ms, while HTTP SSE reports standard OpenAI delta choices and usage chunks.',
];

export interface MockExecuteOptions {
  request: CompletionRequest;
  transport: TransportType;
  pinnedBackend?: string;
  forceErrorType?: 'none' | '429' | '503' | 'midstream' | '400_pin';
  onStart?: (info: { requestId: string; backend: string }) => void;
  onToken?: (text: string) => void;
  onDone?: (metrics: RequestMetrics) => void;
  onError?: (errorDetail: string, code?: number, retryAfter?: string) => void;
}

export interface MockExecutionHandle {
  abort: () => void;
}

export function executeMockInference(options: MockExecuteOptions): MockExecutionHandle {
  const {
    request,
    transport,
    pinnedBackend,
    forceErrorType = 'none',
    onStart,
    onToken,
    onDone,
    onError,
  } = options;

  let isAborted = false;
  let timerId: number | null = null;
  const startTime = performance.now();
  const requestId = `mock-req-${Math.random().toString(36).substring(2, 10)}`;

  // Determine backend
  let backendServed = 'mock-a';
  if (pinnedBackend && pinnedBackend.trim()) {
    const requested = pinnedBackend.trim();
    if (requested !== 'mock-a' && requested !== 'mock-b') {
      // 400 unknown backend error
      setTimeout(() => {
        onError?.(
          `Unknown backend: "${requested}". Available backends: mock-a, mock-b`,
          400
        );
      }, 30);
      return { abort: () => {} };
    }
    if (requested === 'mock-b' && !mockBackendBHealthy) {
      setTimeout(() => {
        mockFailuresB++;
        onError?.(
          `Backend "${requested}" is unhealthy. Request was not sent elsewhere (strict pinning policy).`,
          503
        );
      }, 40);
      return { abort: () => {} };
    }
    backendServed = requested;
  } else {
    // Round-robin between healthy backends
    if (mockBackendBHealthy) {
      backendServed = mockRotationCounter % 2 === 0 ? 'mock-a' : 'mock-b';
      mockRotationCounter++;
    } else {
      backendServed = 'mock-a';
    }
  }

  // Check forced error modes
  if (forceErrorType === '429') {
    setTimeout(() => {
      onError?.(
        'Rate limit exceeded. Too many concurrent tokens requested.',
        429,
        '5'
      );
    }, 60);
    return { abort: () => {} };
  }

  if (forceErrorType === '503') {
    setTimeout(() => {
      onError?.(
        '503 backend_unavailable: No healthy inference backends found to satisfy the request.',
        503
      );
    }, 60);
    return { abort: () => {} };
  }

  // Update mock active count
  if (backendServed === 'mock-a') mockActiveA++;
  else mockActiveB++;

  // Notify start
  setTimeout(() => {
    if (isAborted) return;
    onStart?.({ requestId, backend: backendServed });
  }, 20);

  // If non-streaming, wait delay then emit full text
  if (transport === 'non-streaming') {
    const textIndex = Math.floor(Math.random() * MOCK_SAMPLE_RESPONSES.length);
    const fullText = MOCK_SAMPLE_RESPONSES[textIndex];
    const totalLatency = 240 + Math.floor(Math.random() * 120);

    timerId = window.setTimeout(() => {
      if (isAborted) return;
      if (backendServed === 'mock-a') {
        mockActiveA = Math.max(0, mockActiveA - 1);
        mockCompletedA++;
      } else {
        mockActiveB = Math.max(0, mockActiveB - 1);
        mockCompletedB++;
      }

      onToken?.(fullText);

      const words = fullText.split(/\s+/).filter(Boolean);
      const completionTokens = Math.max(1, words.length * 2);
      const promptTokens = 14;
      const totalTokens = promptTokens + completionTokens;

      const totalLatencyMs = Math.round(performance.now() - startTime);
      // Non-streaming TTFT is not available
      const tokensPerSec = Number(((completionTokens / (totalLatencyMs / 1000))).toFixed(1));

      onDone?.({
        requestId,
        backendServed,
        pinnedBackend,
        pinningMismatchWarning:
          pinnedBackend && pinnedBackend !== backendServed
            ? `requested ${pinnedBackend}, served by ${backendServed}`
            : undefined,
        totalLatencyMs,
        ttftMs: null,
        serverTtftMs: null,
        tokensPerSec,
        completionTokens,
        promptTokens,
        totalTokens,
        finishReason: 'stop',
        usageReported: true,
        timestamp: Date.now(),
        transport,
        model: request.model || 'mock-model',
        status: 'success',
        isMock: true,
      });
    }, totalLatency);

    return {
      abort: () => {
        isAborted = true;
        if (timerId !== null) clearTimeout(timerId);
        if (backendServed === 'mock-a') mockActiveA = Math.max(0, mockActiveA - 1);
        else mockActiveB = Math.max(0, mockActiveB - 1);
      },
    };
  }

  // Streaming (SSE or WS)
  const textIndex = Math.floor(Math.random() * MOCK_SAMPLE_RESPONSES.length);
  const responseWords = MOCK_SAMPLE_RESPONSES[textIndex].split(' ');
  let wordIdx = 0;
  let browserTtftMs: number | null = null;
  const generatedTokens: string[] = [];

  const streamNextWord = () => {
    if (isAborted) return;

    if (forceErrorType === 'midstream' && wordIdx === 5) {
      if (backendServed === 'mock-a') {
        mockActiveA = Math.max(0, mockActiveA - 1);
        mockFailuresA++;
      } else {
        mockActiveB = Math.max(0, mockActiveB - 1);
        mockFailuresB++;
      }
      onError?.('In-band failure: backend connection reset mid-stream (mock simulated error)', 502);
      return;
    }

    if (wordIdx < responseWords.length) {
      const word = (wordIdx === 0 ? '' : ' ') + responseWords[wordIdx];
      generatedTokens.push(word);

      if (browserTtftMs === null) {
        browserTtftMs = Math.max(1, Math.round(performance.now() - startTime));
      }

      onToken?.(word);
      wordIdx++;

      // Delay 20-60 ms per word as requested
      const delay = Math.floor(Math.random() * 41) + 20;
      timerId = window.setTimeout(streamNextWord, delay);
    } else {
      // Done
      if (backendServed === 'mock-a') {
        mockActiveA = Math.max(0, mockActiveA - 1);
        mockCompletedA++;
      } else {
        mockActiveB = Math.max(0, mockActiveB - 1);
        mockCompletedB++;
      }

      const totalLatencyMs = Math.max(1, Math.round(performance.now() - startTime));
      const completionTokens = responseWords.length * 2;
      const promptTokens = 18;
      const totalTokens = promptTokens + completionTokens;

      // Rule: Tokens/sec = completion_tokens / (total latency - TTFT) when streaming
      const streamingDurationSec = (totalLatencyMs - (browserTtftMs ?? 0)) / 1000;
      const tokensPerSec =
        streamingDurationSec > 0
          ? Number((completionTokens / streamingDurationSec).toFixed(1))
          : null;

      const serverTtftMs =
        transport === 'ws'
          ? Math.max(1, (browserTtftMs ?? 20) - 8)
          : null;

      onDone?.({
        requestId,
        backendServed,
        pinnedBackend,
        pinningMismatchWarning:
          pinnedBackend && pinnedBackend !== backendServed
            ? `requested ${pinnedBackend}, served by ${backendServed}`
            : undefined,
        totalLatencyMs,
        ttftMs: browserTtftMs,
        serverTtftMs,
        tokensPerSec,
        completionTokens,
        promptTokens,
        totalTokens,
        finishReason: 'stop',
        usageReported: true,
        timestamp: Date.now(),
        transport,
        model: request.model || 'mock-model',
        status: 'success',
        isMock: true,
      });
    }
  };

  // First delay before TTFT (e.g. 50-120ms)
  const initialDelay = 50 + Math.floor(Math.random() * 70);
  timerId = window.setTimeout(streamNextWord, initialDelay);

  return {
    abort: () => {
      isAborted = true;
      if (timerId !== null) clearTimeout(timerId);
      if (backendServed === 'mock-a') mockActiveA = Math.max(0, mockActiveA - 1);
      else mockActiveB = Math.max(0, mockActiveB - 1);
    },
  };
}
