import { CompletionRequest, WSDoneFrame, WSServerFrame } from './types.ts';

export interface WSExecuteOptions {
  gatewayUrl: string;
  request: CompletionRequest;
  pinnedBackend?: string;
  onStart?: (info: { requestId: string; backend: string }) => void;
  onToken?: (text: string) => void;
  onDone?: (
    frame: WSDoneFrame,
    metrics: {
      totalLatencyMs: number;
      browserTtftMs: number | null;
      serverTtftMs: number | null;
    }
  ) => void;
  onError?: (errorMessage: string, code?: number, requestId?: string) => void;
  onClose?: () => void;
}

export interface WSHandle {
  abort: () => void;
}

/**
 * Converts HTTP/HTTPS URL into WS/WSS URL with /v1/chat/stream path.
 */
export function getWebSocketUrl(baseUrl: string, pinnedBackend?: string): string {
  let wsUrl = baseUrl.trim();
  if (!wsUrl) {
    // Same origin as the page (UI and API behind one reverse proxy).
    wsUrl = (window.location.protocol === 'https:' ? 'wss://' : 'ws://') + window.location.host;
  }

  // Convert scheme
  if (wsUrl.startsWith('https://')) {
    wsUrl = 'wss://' + wsUrl.slice(8);
  } else if (wsUrl.startsWith('http://')) {
    wsUrl = 'ws://' + wsUrl.slice(7);
  } else if (!wsUrl.startsWith('ws://') && !wsUrl.startsWith('wss://')) {
    wsUrl = (window.location.protocol === 'https:' ? 'wss://' : 'ws://') + wsUrl;
  }

  // Strip trailing slashes
  wsUrl = wsUrl.replace(/\/+$/, '');

  // Append path
  wsUrl += '/v1/chat/stream';

  // Append ?backend=<pinnedBackend> if requested
  if (pinnedBackend && pinnedBackend.trim()) {
    wsUrl += `?backend=${encodeURIComponent(pinnedBackend.trim())}`;
  }

  return wsUrl;
}

/**
 * Executes a WebSocket inference call against the gateway.
 */
export function executeWebSocketInference(options: WSExecuteOptions): WSHandle {
  const {
    gatewayUrl,
    request,
    pinnedBackend,
    onStart,
    onToken,
    onDone,
    onError,
    onClose,
  } = options;

  let ws: WebSocket | null = null;
  let userAborted = false;
  let isDone = false;
  let firstTokenReceived = false;
  let browserTtftMs: number | null = null;

  const startTime = performance.now();
  const wsEndpoint = getWebSocketUrl(gatewayUrl, pinnedBackend);

  try {
    ws = new WebSocket(wsEndpoint);
  } catch (err) {
    const msg = err instanceof Error ? err.message : 'Failed to instantiate WebSocket';
    onError?.(`WebSocket connection error: ${msg}`);
    return { abort: () => {} };
  }

  ws.onopen = () => {
    if (userAborted) {
      ws?.close(1000, 'Aborted by user');
      return;
    }
    // Send ONE JSON message as required by InferMesh gateway specification
    const payload = {
      ...request,
      stream: true,
    };
    ws?.send(JSON.stringify(payload));
  };

  ws.onmessage = (event) => {
    if (userAborted) return;

    let frame: WSServerFrame;
    try {
      frame = JSON.parse(event.data);
    } catch {
      onError?.(`Unparseable WebSocket frame: ${event.data}`);
      return;
    }

    switch (frame.type) {
      case 'start': {
        onStart?.({
          requestId: frame.request_id,
          backend: frame.backend,
        });
        break;
      }
      case 'token': {
        if (!firstTokenReceived) {
          firstTokenReceived = true;
          browserTtftMs = Math.max(0, Math.round(performance.now() - startTime));
        }
        onToken?.(frame.text);
        break;
      }
      case 'done': {
        isDone = true;
        const totalLatencyMs = Math.max(1, Math.round(performance.now() - startTime));
        const serverTtftMs = typeof frame.ttft_ms === 'number' ? frame.ttft_ms : null;

        onDone?.(frame, {
          totalLatencyMs,
          browserTtftMs,
          serverTtftMs,
        });
        break;
      }
      case 'error': {
        isDone = true;
        const errorText =
          typeof frame.error === 'string'
            ? frame.error
            : frame.error?.message || 'Gateway WebSocket Error';
        onError?.(errorText, undefined, frame.request_id);
        break;
      }
      default: {
        console.warn('Unknown WebSocket frame:', frame);
      }
    }
  };

  ws.onerror = () => {
    if (userAborted) return;
    if (!isDone) {
      onError?.(
        'WebSocket connection failed. Verify the gateway is running and WS_ALLOWED_ORIGINS includes this origin.'
      );
    }
  };

  ws.onclose = (event) => {
    onClose?.();
    if (!isDone && !userAborted && event.code !== 1000) {
      onError?.(
        `WebSocket closed unexpectedly (code: ${event.code}${event.reason ? `, reason: ${event.reason}` : ''})`
      );
    }
  };

  return {
    abort: () => {
      userAborted = true;
      if (ws && (ws.readyState === WebSocket.OPEN || ws.readyState === WebSocket.CONNECTING)) {
        ws.close(1000, 'Client aborted');
      }
    },
  };
}
