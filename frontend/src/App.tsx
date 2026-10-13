import React, { useState, useRef, useCallback, useEffect } from 'react';
import { ChatMessage, RequestLogEntry, RequestMetrics } from './api/types.ts';
import { executeInference, ClientExecutionHandle } from './api/client.ts';
import { buildPayloadMessages } from './api/payload.ts';
import { useInferMeshSettings } from './hooks/useInferMeshSettings.ts';
import { useGatewayStatus } from './hooks/useGatewayStatus.ts';
import { Header } from './components/Header.tsx';
import { MockBanner } from './components/MockBanner.tsx';
import { Sidebar } from './components/Sidebar.tsx';
import { ChatThread } from './components/ChatThread.tsx';
import { ChatInput } from './components/ChatInput.tsx';
import { RequestLogTable } from './components/RequestLogTable.tsx';
import { SettingsModal } from './components/SettingsModal.tsx';
import { Menu } from 'lucide-react';

export default function App() {
  const { settings, updateSetting, resetSettings } = useInferMeshSettings();
  const {
    backendsData,
    readyData,
    modelsData,
    modelsLoaded,
    gatewayError,
    refreshNow,
  } = useGatewayStatus(settings.gatewayUrl, settings.isMock);

  const [activeTab, setActiveTab] = useState<'console' | 'logs'>('console');
  const [messages, setMessages] = useState<ChatMessage[]>([]);
  const [requestLogs, setRequestLogs] = useState<RequestLogEntry[]>([]);
  const [isStreaming, setIsStreaming] = useState(false);
  const [activeRequestId, setActiveRequestId] = useState<string | undefined>();
  const [activeBackend, setActiveBackend] = useState<string | undefined>();
  const [isSidebarOpen, setIsSidebarOpen] = useState(false);
  const [isSettingsOpen, setIsSettingsOpen] = useState(false);

  const executionRef = useRef<ClientExecutionHandle | null>(null);
  const assistantMsgIdRef = useRef<string | null>(null);

  // Facts about the request in flight. A ref (not state) so the callbacks below, which are
  // created when the request starts, always read the current values when it ends.
  const liveRef = useRef<{
    requestId?: string;
    backend?: string;
    startPerf: number;
    startWall: number;
    ttftMs: number | null;
  } | null>(null);

  // Connected to a real gateway: make sure the selected model is one the gateway offers. The
  // persisted or default value is often a leftover (for example the mock model name).
  useEffect(() => {
    if (settings.isMock || !modelsLoaded) return;
    if (modelsData.length > 0) {
      if (!modelsData.some((m) => m.id === settings.model)) {
        updateSetting('model', modelsData[0].id);
      }
    } else if (settings.model.startsWith('mock-')) {
      updateSetting('model', '');
    }
  }, [settings.isMock, settings.model, modelsLoaded, modelsData, updateSetting]);

  /** Appends a row to the request log for a request that ended without a metrics object. */
  const logEnded = useCallback(
    (status: 'error' | 'aborted', detail: string, promptText: string, retryAfter?: string | null) => {
      const live = liveRef.current;
      const entry: RequestLogEntry = {
        id: `log-${Date.now()}-${Math.random().toString(36).slice(2, 6)}`,
        requestId: live?.requestId || 'n/a',
        backendServed: live?.backend || settings.pinnedBackend || 'unassigned',
        pinnedBackend: settings.pinnedBackend || undefined,
        totalLatencyMs: live ? Math.max(1, Math.round(performance.now() - live.startPerf)) : 0,
        ttftMs: live?.ttftMs ?? null,
        serverTtftMs: null,
        tokensPerSec: null,
        completionTokens: null,
        promptTokens: null,
        totalTokens: null,
        finishReason: status === 'error' ? 'error' : 'abort',
        usageReported: false,
        timestamp: live?.startWall ?? Date.now(),
        transport: settings.transport,
        model: settings.model,
        systemPrompt: settings.systemPrompt.trim() || undefined,
        status,
        errorDetail: detail,
        retryAfter,
        isMock: settings.isMock,
        promptPreview: promptText.slice(0, 80),
        responsePreview: '',
      };
      setRequestLogs((prev) => [entry, ...prev]);
    },
    [settings]
  );

  const lastPromptRef = useRef('');

  // Send a new inference request
  const handleSendMessage = useCallback(
    (promptText: string) => {
      if (isStreaming) return;

      const userMsgId = `user-${Date.now()}`;
      const assistantMsgId = `asst-${Date.now()}`;
      assistantMsgIdRef.current = assistantMsgId;
      lastPromptRef.current = promptText;
      liveRef.current = { startPerf: performance.now(), startWall: Date.now(), ttftMs: null };

      const newUserMsg: ChatMessage = {
        id: userMsgId,
        role: 'user',
        content: promptText,
        timestamp: Date.now(),
      };

      const newAssistantMsg: ChatMessage = {
        id: assistantMsgId,
        role: 'assistant',
        content: '',
        timestamp: Date.now(),
        isStreaming: true,
      };

      setMessages((prev) => [...prev, newUserMsg, newAssistantMsg]);
      setIsStreaming(true);
      setActiveRequestId(undefined);
      setActiveBackend(undefined);

      // The system prompt is always messages[0] with role 'system', for every transport.
      const payloadMessages = buildPayloadMessages(settings.systemPrompt, messages, promptText);

      const handle = executeInference({
        gatewayUrl: settings.gatewayUrl,
        isMock: settings.isMock,
        transport: settings.transport,
        pinnedBackend: settings.pinnedBackend || undefined,
        forceMockError: settings.forceMockError,
        request: {
          model: settings.model,
          messages: payloadMessages,
          max_tokens: settings.maxTokens,
          temperature: settings.temperature,
        },
        onStart: (info) => {
          if (liveRef.current) {
            liveRef.current.requestId = info.requestId;
            liveRef.current.backend = info.backend;
          }
          setActiveRequestId(info.requestId);
          setActiveBackend(info.backend);
        },
        onToken: (token) => {
          if (liveRef.current && liveRef.current.ttftMs === null && token) {
            liveRef.current.ttftMs = Math.max(1, Math.round(performance.now() - liveRef.current.startPerf));
          }
          setMessages((prev) =>
            prev.map((m) =>
              m.id === assistantMsgId ? { ...m, content: m.content + token } : m
            )
          );
        },
        onDone: (metrics) => {
          setIsStreaming(false);
          setActiveRequestId(undefined);
          setActiveBackend(undefined);
          executionRef.current = null;

          setMessages((prev) =>
            prev.map((m) =>
              m.id === assistantMsgId
                ? { ...m, isStreaming: false, metrics }
                : m
            )
          );

          // Add to session request log; the timestamp is when the request started.
          const logEntry: RequestLogEntry = {
            ...metrics,
            timestamp: liveRef.current?.startWall ?? metrics.timestamp,
            id: `log-${Date.now()}-${Math.random().toString(36).slice(2, 6)}`,
            promptPreview: promptText.slice(0, 80),
            responsePreview: '',
            systemPrompt: settings.systemPrompt.trim() || undefined,
          };
          setRequestLogs((prev) => [logEntry, ...prev]);
          liveRef.current = null;

          // Refresh backends telemetry after completed request
          refreshNow();
        },
        onError: (errorDetail, _code, retryAfter, info) => {
          // A failed request can still carry the gateway's request ID (response header / WS error frame).
          if (liveRef.current && info) {
            liveRef.current.requestId = liveRef.current.requestId || info.requestId;
            liveRef.current.backend = liveRef.current.backend || info.backend;
          }
          setIsStreaming(false);
          setActiveRequestId(undefined);
          setActiveBackend(undefined);
          executionRef.current = null;

          setMessages((prev) =>
            prev.map((m) =>
              m.id === assistantMsgId
                ? { ...m, isStreaming: false, error: errorDetail }
                : m
            )
          );

          // Failed requests still get a log row, with the real request ID, backend and elapsed time.
          logEnded('error', errorDetail, promptText, retryAfter);
          liveRef.current = null;
          refreshNow();
        },
        onClose: () => {
          setIsStreaming(false);
          executionRef.current = null;
        },
      });

      executionRef.current = handle;
    },
    [isStreaming, settings, messages, refreshNow, logEnded]
  );

  // Stop button handler
  const handleStop = useCallback(() => {
    if (!executionRef.current) return; // nothing in flight (avoids logging a phantom abort)
    executionRef.current.abort();
    executionRef.current = null;
    setIsStreaming(false);

    const currentId = assistantMsgIdRef.current;
    if (currentId) {
      setMessages((prev) =>
        prev.map((m) => (m.id === currentId ? { ...m, isStreaming: false, aborted: true } : m))
      );
      logEnded('aborted', 'Client aborted request before completion', lastPromptRef.current);
    }
    liveRef.current = null;
    setActiveRequestId(undefined);
    setActiveBackend(undefined);
  }, [logEnded]);

  const handleNewChat = useCallback(() => {
    if (isStreaming) {
      handleStop();
    }
    setMessages([]);
  }, [isStreaming, handleStop]);

  return (
    <div className="min-h-screen flex flex-col bg-neutral-50 dark:bg-neutral-950 text-neutral-900 dark:text-neutral-100 font-sans antialiased">
      {/* Mock Mode Persistent Banner */}
      {settings.isMock && (
        <MockBanner onOpenSettings={() => setIsSettingsOpen(true)} />
      )}

      {/* Main Top Header */}
      <Header
        readyData={readyData}
        activeTab={activeTab}
        onTabChange={setActiveTab}
        requestCount={requestLogs.length}
        onNewChat={handleNewChat}
        onOpenSettings={() => setIsSettingsOpen(true)}
        isMock={settings.isMock}
      />

      {/* Mobile bar with Sidebar toggle */}
      <div className="lg:hidden flex items-center justify-between px-4 py-2 bg-white dark:bg-neutral-900 border-b border-neutral-200 dark:border-neutral-800 text-xs">
        <button
          onClick={() => setIsSidebarOpen(true)}
          className="flex items-center gap-1.5 px-2.5 py-1.5 rounded-md border border-neutral-200 dark:border-neutral-700 bg-neutral-50 dark:bg-neutral-800 text-neutral-700 dark:text-neutral-300 font-medium"
        >
          <Menu className="w-3.5 h-3.5" />
          <span>Fleet & Controls</span>
        </button>

        <div className="font-mono text-neutral-500 text-[11px]">
          Target: {settings.pinnedBackend || 'Auto router'}
        </div>
      </div>

      {/* Core Workspace Layout */}
      <div className="flex-1 flex overflow-hidden">
        {/* Left Sidebar */}
        <Sidebar
          settings={settings}
          updateSetting={updateSetting}
          backendsData={backendsData}
          modelsData={modelsData}
          modelsLoaded={modelsLoaded}
          gatewayError={gatewayError}
          onRefreshBackends={refreshNow}
          isOpen={isSidebarOpen}
          onClose={() => setIsSidebarOpen(false)}
        />

        {/* Center Main Viewport */}
        <main className="flex-1 flex flex-col h-[calc(100vh-3.5rem)] overflow-hidden">
          {activeTab === 'console' ? (
            <div className="flex-1 flex flex-col h-full overflow-hidden">
              <ChatThread
                messages={messages}
                isStreaming={isStreaming}
                systemPrompt={settings.systemPrompt}
                activeRequestId={activeRequestId}
                activeBackend={activeBackend}
                onSendPreset={handleSendMessage}
                onOpenSidebar={() => setIsSidebarOpen(true)}
              />
              <ChatInput
                onSend={handleSendMessage}
                onStop={handleStop}
                isStreaming={isStreaming}
              />
            </div>
          ) : (
            <RequestLogTable
              logs={requestLogs}
              onClearLogs={() => setRequestLogs([])}
            />
          )}
        </main>
      </div>

      {/* Gateway Connection & Settings Modal */}
      <SettingsModal
        isOpen={isSettingsOpen}
        onClose={() => setIsSettingsOpen(false)}
        settings={settings}
        updateSetting={updateSetting}
        onReset={resetSettings}
      />
    </div>
  );
}
