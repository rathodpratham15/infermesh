import React, { useState } from 'react';
import { BackendsResponse, ModelItem, TransportType } from '../api/types.ts';
import { ConsoleSettings } from '../hooks/useInferMeshSettings.ts';
import {
  Server,
  Layers,
  Cpu,
  Radio,
  SlidersHorizontal,
  ChevronDown,
  ChevronUp,
  RefreshCw,
  AlertCircle,
  HelpCircle,
} from 'lucide-react';

interface SidebarProps {
  settings: ConsoleSettings;
  updateSetting: <K extends keyof ConsoleSettings>(key: K, value: ConsoleSettings[K]) => void;
  backendsData: BackendsResponse | null;
  modelsData: ModelItem[];
  modelsLoaded: boolean;
  gatewayError: string | null;
  onRefreshBackends: () => void;
  isOpen: boolean;
  onClose: () => void;
}

export const Sidebar: React.FC<SidebarProps> = ({
  settings,
  updateSetting,
  backendsData,
  modelsData,
  modelsLoaded,
  gatewayError,
  onRefreshBackends,
  isOpen,
  onClose,
}) => {
  const [showSystemPrompt, setShowSystemPrompt] = useState(Boolean(settings.systemPrompt));
  const [customModelMode, setCustomModelMode] = useState(false);

  const backends = backendsData?.backends || [];
  const queueDepth = backendsData?.queue_depth ?? 0;

  const hasModels = modelsData.length > 0;
  const isFreeTextModel = !hasModels || customModelMode;

  return (
    <>
      {/* Mobile backdrop */}
      {isOpen && (
        <div
          className="fixed inset-0 bg-neutral-900/40 z-30 lg:hidden backdrop-blur-xs"
          onClick={onClose}
        />
      )}

      <aside
        className={`fixed lg:static top-14 bottom-0 left-0 z-40 w-80 bg-white dark:bg-neutral-900 border-r border-neutral-200 dark:border-neutral-800 flex flex-col transition-transform duration-200 ease-in-out ${
          isOpen ? 'translate-x-0' : '-translate-x-full lg:translate-x-0'
        }`}
      >
        <div className="flex-1 overflow-y-auto p-4 space-y-6">
          {/* SECTION 1: Backend Fleet Telemetry */}
          <section className="space-y-3">
            <div className="flex items-center justify-between">
              <div className="flex items-center gap-1.5 text-xs font-semibold uppercase tracking-wider text-neutral-500 dark:text-neutral-400">
                <Server className="w-3.5 h-3.5" />
                <span>Backend Fleet</span>
              </div>
              <div className="flex items-center gap-2">
                <span className="text-[11px] font-mono text-neutral-500 dark:text-neutral-400">
                  Queue: <strong className="font-semibold text-neutral-800 dark:text-neutral-200">{queueDepth}</strong>
                </span>
                <button
                  onClick={onRefreshBackends}
                  className="p-1 text-neutral-400 hover:text-neutral-700 dark:hover:text-neutral-200 rounded transition-colors"
                  title="Refresh /v1/backends"
                >
                  <RefreshCw className="w-3 h-3" />
                </button>
              </div>
            </div>

            {gatewayError && (
              <div className="p-2.5 rounded-md bg-rose-50 dark:bg-rose-950/40 border border-rose-200 dark:border-rose-900/60 text-xs text-rose-700 dark:text-rose-300">
                <div className="flex items-start gap-1.5">
                  <AlertCircle className="w-3.5 h-3.5 shrink-0 mt-0.5" />
                  <span className="leading-tight">{gatewayError}</span>
                </div>
              </div>
            )}

            {backends.length === 0 && !gatewayError ? (
              <div className="p-3 text-center text-xs text-neutral-400 border border-dashed border-neutral-200 dark:border-neutral-800 rounded-md">
                Connecting to /v1/backends...
              </div>
            ) : (
              <div className="space-y-2">
                {backends.map((b) => {
                  const utilization = b.max_concurrent > 0 ? (b.active / b.max_concurrent) * 100 : 0;
                  const isPinned = settings.pinnedBackend === b.name;

                  return (
                    <div
                      key={b.name}
                      className={`p-2.5 rounded-lg border text-xs font-mono transition-all ${
                        isPinned
                          ? 'border-neutral-900 dark:border-neutral-100 bg-neutral-50 dark:bg-neutral-800/80 shadow-xs'
                          : 'border-neutral-200 dark:border-neutral-800/80 bg-neutral-50/50 dark:bg-neutral-900/60'
                      }`}
                    >
                      <div className="flex items-center justify-between mb-1.5 font-sans">
                        <div className="flex items-center gap-1.5 min-w-0">
                          <span
                            className={`w-2 h-2 rounded-full shrink-0 ${
                              b.healthy ? 'bg-emerald-500' : 'bg-rose-500'
                            }`}
                            aria-hidden="true"
                          />
                          <span className="font-semibold text-neutral-900 dark:text-neutral-100 truncate">
                            {b.name}
                          </span>
                          {isPinned && (
                            <span className="text-[10px] font-mono px-1 py-0.2 rounded bg-neutral-200 dark:bg-neutral-700 text-neutral-800 dark:text-neutral-200">
                              pinned
                            </span>
                          )}
                        </div>
                        <span className="text-[11px] font-mono text-neutral-500 dark:text-neutral-400 tabular-nums">
                          {b.latency_ewma_ms.toFixed(1)} ms
                        </span>
                      </div>

                      {/* Active concurrency progress bar */}
                      <div className="space-y-1">
                        <div className="flex justify-between text-[11px] text-neutral-500 dark:text-neutral-400">
                          <span>Active</span>
                          <span className="tabular-nums">
                            {b.active} / {b.max_concurrent} ({utilization.toFixed(0)}%)
                          </span>
                        </div>
                        <div className="h-1.5 w-full bg-neutral-200 dark:bg-neutral-800 rounded-full overflow-hidden">
                          <div
                            className={`h-full transition-all duration-300 rounded-full ${
                              utilization > 80
                                ? 'bg-rose-500'
                                : utilization > 50
                                ? 'bg-amber-500'
                                : 'bg-neutral-900 dark:bg-neutral-200'
                            }`}
                            style={{ width: `${Math.min(100, Math.max(4, utilization))}%` }}
                          />
                        </div>
                      </div>

                      <div className="mt-2 pt-1.5 border-t border-neutral-200/60 dark:border-neutral-800/60 flex items-center justify-between text-[10px] text-neutral-400">
                        <span>Done: {b.completed}</span>
                        <span>Failures: {b.failures}</span>
                        <span>Weight: {b.weight}</span>
                      </div>
                    </div>
                  );
                })}
              </div>
            )}
          </section>

          {/* SECTION 2: Routing Controls */}
          <section className="space-y-4 pt-2 border-t border-neutral-200 dark:border-neutral-800">
            <div className="flex items-center gap-1.5 text-xs font-semibold uppercase tracking-wider text-neutral-500 dark:text-neutral-400">
              <SlidersHorizontal className="w-3.5 h-3.5" />
              <span>Routing & Dispatch</span>
            </div>

            {/* Backend Selector & Pinning */}
            <div className="space-y-1.5">
              <div className="flex items-center justify-between">
                <label className="text-xs font-medium text-neutral-700 dark:text-neutral-300">
                  Target Backend
                </label>
                <span
                  className="text-[11px] text-neutral-400 cursor-help flex items-center gap-0.5"
                  title="Request header X-InferMesh-Pin-Backend pins an HTTP request. For WebSocket, ?backend= is appended. If backend is unhealthy, strict pinning returns 503."
                >
                  <span>Pinning</span>
                  <HelpCircle className="w-3 h-3" />
                </span>
              </div>
              <select
                value={settings.pinnedBackend}
                onChange={(e) => updateSetting('pinnedBackend', e.target.value)}
                className="w-full text-xs bg-white dark:bg-neutral-800 border border-neutral-200 dark:border-neutral-700 rounded-md px-2.5 py-1.5 text-neutral-900 dark:text-neutral-100 focus:outline-hidden focus:ring-1 focus:ring-neutral-400"
              >
                <option value="">Auto (router decides)</option>
                {backends.map((b) => (
                  <option key={b.name} value={b.name}>
                    Pin: {b.name} {b.healthy ? '' : '(unhealthy)'}
                  </option>
                ))}
              </select>
            </div>

            {/* Transport Selector */}
            <div className="space-y-1.5">
              <label className="text-xs font-medium text-neutral-700 dark:text-neutral-300 flex items-center justify-between">
                <span>Transport</span>
                <span className="text-[10px] text-neutral-400 uppercase font-mono">
                  {settings.transport}
                </span>
              </label>
              <div className="grid grid-cols-3 gap-1 p-1 bg-neutral-100 dark:bg-neutral-800 rounded-lg text-xs">
                {(['sse', 'ws', 'non-streaming'] as TransportType[]).map((t) => (
                  <button
                    key={t}
                    type="button"
                    onClick={() => updateSetting('transport', t)}
                    className={`py-1.5 px-1 font-medium rounded-md text-[11px] transition-colors whitespace-nowrap text-center ${
                      settings.transport === t
                        ? 'bg-white dark:bg-neutral-900 text-neutral-900 dark:text-neutral-100 shadow-xs'
                        : 'text-neutral-600 dark:text-neutral-400 hover:text-neutral-900 dark:hover:text-neutral-200'
                    }`}
                  >
                    {t === 'sse' ? 'SSE' : t === 'ws' ? 'WebSocket' : 'Non-stream'}
                  </button>
                ))}
              </div>
            </div>

            {/* Model Selector */}
            <div className="space-y-1.5">
              <div className="flex items-center justify-between">
                <label className="text-xs font-medium text-neutral-700 dark:text-neutral-300">
                  Model
                </label>
                {hasModels && (
                  <button
                    type="button"
                    onClick={() => setCustomModelMode((prev) => !prev)}
                    className="text-[11px] text-neutral-500 hover:text-neutral-800 dark:hover:text-neutral-200 underline"
                  >
                    {customModelMode ? 'Pick from /v1/models' : 'Free-text input'}
                  </button>
                )}
              </div>

              {isFreeTextModel ? (
                <div className="space-y-1">
                  <input
                    type="text"
                    value={settings.model}
                    onChange={(e) => updateSetting('model', e.target.value)}
                    placeholder="Enter model name (e.g. llama-3.1-8b)"
                    className="w-full text-xs bg-white dark:bg-neutral-800 border border-neutral-200 dark:border-neutral-700 rounded-md px-2.5 py-1.5 text-neutral-900 dark:text-neutral-100 focus:outline-hidden focus:ring-1 focus:ring-neutral-400 font-mono"
                  />
                  {!hasModels && modelsLoaded && (
                    <p className="text-[10px] text-neutral-400">
                      /v1/models returned empty or 404 (using free-text input)
                    </p>
                  )}
                </div>
              ) : (
                <select
                  value={settings.model}
                  onChange={(e) => updateSetting('model', e.target.value)}
                  className="w-full text-xs bg-white dark:bg-neutral-800 border border-neutral-200 dark:border-neutral-700 rounded-md px-2.5 py-1.5 text-neutral-900 dark:text-neutral-100 focus:outline-hidden focus:ring-1 focus:ring-neutral-400 font-mono"
                >
                  {modelsData.map((m) => (
                    <option key={m.id} value={m.id}>
                      {m.id} {m.backend ? `(${m.backend})` : ''}
                    </option>
                  ))}
                </select>
              )}
            </div>

            {/* Max Tokens */}
            <div className="space-y-1.5">
              <div className="flex justify-between items-center text-xs">
                <span className="font-medium text-neutral-700 dark:text-neutral-300">
                  max_tokens
                </span>
                <span className="font-mono text-neutral-500 tabular-nums">
                  {settings.maxTokens}
                </span>
              </div>
              <input
                type="range"
                min={32}
                max={4096}
                step={32}
                value={settings.maxTokens}
                onChange={(e) => updateSetting('maxTokens', Number(e.target.value))}
                className="w-full accent-neutral-900 dark:accent-neutral-100 h-1.5 bg-neutral-200 dark:bg-neutral-700 rounded-lg cursor-pointer"
              />
            </div>

            {/* Temperature */}
            <div className="space-y-1.5">
              <div className="flex justify-between items-center text-xs">
                <span className="font-medium text-neutral-700 dark:text-neutral-300">
                  temperature
                </span>
                <span className="font-mono text-neutral-500 tabular-nums">
                  {settings.temperature.toFixed(2)}
                </span>
              </div>
              <input
                type="range"
                min={0}
                max={2.0}
                step={0.05}
                value={settings.temperature}
                onChange={(e) => updateSetting('temperature', Number(e.target.value))}
                className="w-full accent-neutral-900 dark:accent-neutral-100 h-1.5 bg-neutral-200 dark:bg-neutral-700 rounded-lg cursor-pointer"
              />
            </div>

            {/* System Prompt (Multi-line input) */}
            <div className="space-y-1.5 pt-1">
              <div className="flex items-center justify-between">
                <label className="text-xs font-medium text-neutral-700 dark:text-neutral-300">
                  System Prompt
                </label>
                {settings.systemPrompt.trim() && (
                  <button
                    type="button"
                    onClick={() => updateSetting('systemPrompt', '')}
                    className="text-[10px] text-neutral-400 hover:text-neutral-600 dark:hover:text-neutral-200 underline"
                  >
                    Clear
                  </button>
                )}
              </div>
              <textarea
                value={settings.systemPrompt}
                onChange={(e) => updateSetting('systemPrompt', e.target.value)}
                placeholder="System instructions sent as first message with role: 'system'..."
                rows={4}
                className="w-full text-xs bg-white dark:bg-neutral-800 border border-neutral-200 dark:border-neutral-700 rounded-md p-2 text-neutral-900 dark:text-neutral-100 placeholder:text-neutral-400 focus:outline-hidden focus:ring-1 focus:ring-neutral-400 font-sans resize-y min-h-[76px]"
              />
              <div className="flex items-center justify-between text-[10px] text-neutral-400">
                <span>Sent as messages[0] with role 'system'</span>
                <span className="font-mono tabular-nums">{settings.systemPrompt.length} chars</span>
              </div>
            </div>

            {/* Mock Mode Failure Simulator (Only visible in Mock Mode) */}
            {settings.isMock && (
              <div className="p-2.5 rounded-lg bg-amber-500/10 border border-amber-500/20 space-y-1.5 text-xs">
                <div className="flex items-center justify-between text-amber-900 dark:text-amber-200 font-medium">
                  <span>Simulate Gateway Fault</span>
                </div>
                <select
                  value={settings.forceMockError}
                  onChange={(e) =>
                    updateSetting(
                      'forceMockError',
                      e.target.value as ConsoleSettings['forceMockError']
                    )
                  }
                  className="w-full text-xs bg-white dark:bg-neutral-900 border border-amber-500/30 rounded px-2 py-1 text-neutral-800 dark:text-neutral-200 focus:outline-hidden font-mono"
                >
                  <option value="none">Nominal (Success)</option>
                  <option value="429">429 Rate Limit (Retry-After: 5s)</option>
                  <option value="503">503 Backend Unavailable</option>
                  <option value="midstream">Mid-stream In-Band Error</option>
                </select>
                <p className="text-[10px] text-amber-700/80 dark:text-amber-400/80 leading-normal">
                  Test error display and session log behavior for gateway failure scenarios.
                </p>
              </div>
            )}
          </section>
        </div>
      </aside>
    </>
  );
};
