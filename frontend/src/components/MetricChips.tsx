import React, { useState } from 'react';
import { RequestMetrics } from '../api/types.ts';
import { Copy, Check, AlertTriangle, HelpCircle } from 'lucide-react';

interface MetricChipsProps {
  metrics: RequestMetrics;
}

export const MetricChips: React.FC<MetricChipsProps> = ({ metrics }) => {
  const [copiedId, setCopiedId] = useState(false);

  const handleCopyId = () => {
    if (!metrics.requestId || metrics.requestId === 'n/a') return;
    navigator.clipboard.writeText(metrics.requestId);
    setCopiedId(true);
    setTimeout(() => setCopiedId(false), 2000);
  };

  const hasPinningMismatch = Boolean(metrics.pinningMismatchWarning);

  return (
    <div className="mt-2.5 pt-2.5 border-t border-neutral-100 dark:border-neutral-800/80">
      {/* Warning for backend pinning mismatch */}
      {hasPinningMismatch && (
        <div className="mb-2 p-2 bg-amber-500/10 border border-amber-500/25 rounded-md flex items-center gap-2 text-xs text-amber-800 dark:text-amber-300 font-mono">
          <AlertTriangle className="w-4 h-4 text-amber-600 dark:text-amber-400 shrink-0" />
          <span>Pinning warning: {metrics.pinningMismatchWarning}</span>
        </div>
      )}

      <div className="flex flex-wrap items-center gap-1.5 text-xs font-mono text-neutral-600 dark:text-neutral-400">
        {/* Request ID (clickable to copy) */}
        <button
          onClick={handleCopyId}
          className="inline-flex items-center gap-1 px-2 py-0.5 rounded bg-neutral-100 dark:bg-neutral-800 hover:bg-neutral-200 dark:hover:bg-neutral-700 text-neutral-800 dark:text-neutral-200 transition-colors cursor-pointer select-none"
          title="Click to copy Request ID"
        >
          <span className="text-neutral-400 dark:text-neutral-500 font-sans text-[11px]">ID:</span>
          <span className="truncate max-w-[130px]">{metrics.requestId}</span>
          {copiedId ? (
            <Check className="w-3 h-3 text-emerald-600 dark:text-emerald-400" />
          ) : (
            <Copy className="w-3 h-3 text-neutral-400" />
          )}
        </button>

        {/* Backend Served */}
        <div
          className={`inline-flex items-center gap-1 px-2 py-0.5 rounded ${
            hasPinningMismatch
              ? 'bg-amber-100 dark:bg-amber-950/60 text-amber-800 dark:text-amber-300 border border-amber-300/40'
              : 'bg-neutral-100 dark:bg-neutral-800 text-neutral-800 dark:text-neutral-200'
          }`}
        >
          <span className="text-neutral-400 dark:text-neutral-500 font-sans text-[11px]">Backend:</span>
          <span className="font-semibold">{metrics.backendServed}</span>
          {metrics.pinnedBackend && (
            <span className="text-[10px] text-neutral-400">
              (pin: {metrics.pinnedBackend})
            </span>
          )}
        </div>

        {/* Total Latency */}
        <div className="inline-flex items-center gap-1 px-2 py-0.5 rounded bg-neutral-100 dark:bg-neutral-800 text-neutral-800 dark:text-neutral-200">
          <span className="text-neutral-400 dark:text-neutral-500 font-sans text-[11px]">Latency:</span>
          <span className="tabular-nums font-semibold">{metrics.totalLatencyMs}ms</span>
        </div>

        {/* TTFT (Browser measured) */}
        <div className="inline-flex items-center gap-1 px-2 py-0.5 rounded bg-neutral-100 dark:bg-neutral-800 text-neutral-800 dark:text-neutral-200">
          <span className="text-neutral-400 dark:text-neutral-500 font-sans text-[11px]">TTFT:</span>
          {metrics.ttftMs !== null ? (
            <span className="tabular-nums font-semibold">{metrics.ttftMs}ms</span>
          ) : (
            <span className="text-neutral-400 italic">n/a</span>
          )}
        </div>

        {/* Server reported TTFT for WebSocket */}
        {metrics.transport === 'ws' && metrics.serverTtftMs !== null && metrics.serverTtftMs !== undefined && (
          <div
            className="inline-flex items-center gap-1 px-2 py-0.5 rounded bg-sky-50 dark:bg-sky-950/40 text-sky-800 dark:text-sky-300 border border-sky-200/40"
            title="Server-reported TTFT from WebSocket done frame"
          >
            <span className="text-sky-500 dark:text-sky-400 font-sans text-[11px]">Server TTFT:</span>
            <span className="tabular-nums font-semibold">{metrics.serverTtftMs}ms</span>
          </div>
        )}

        {/* Tokens / sec */}
        <div className="inline-flex items-center gap-1 px-2 py-0.5 rounded bg-neutral-100 dark:bg-neutral-800 text-neutral-800 dark:text-neutral-200">
          <span className="text-neutral-400 dark:text-neutral-500 font-sans text-[11px]">Tokens/s:</span>
          {metrics.tokensPerSec !== null ? (
            <span className="tabular-nums font-semibold">{metrics.tokensPerSec}</span>
          ) : (
            <span
              className="inline-flex items-center gap-0.5 text-neutral-400 italic cursor-help"
              title={metrics.tokensPerSecReason || 'server did not report token usage'}
            >
              <span>n/a</span>
              <HelpCircle className="w-3 h-3 text-neutral-400" />
            </span>
          )}
        </div>

        {/* Output Tokens */}
        <div className="inline-flex items-center gap-1 px-2 py-0.5 rounded bg-neutral-100 dark:bg-neutral-800 text-neutral-800 dark:text-neutral-200">
          <span className="text-neutral-400 dark:text-neutral-500 font-sans text-[11px]">Output:</span>
          {metrics.completionTokens !== null ? (
            <span className="tabular-nums">{metrics.completionTokens} tok</span>
          ) : (
            <span className="text-neutral-400 italic">n/a</span>
          )}
        </div>

        {/* Finish Reason */}
        <div className="inline-flex items-center gap-1 px-2 py-0.5 rounded bg-neutral-100 dark:bg-neutral-800 text-neutral-800 dark:text-neutral-200">
          <span className="text-neutral-400 dark:text-neutral-500 font-sans text-[11px]">Finish:</span>
          <span className="text-neutral-700 dark:text-neutral-300">
            {metrics.finishReason || 'unknown'}
          </span>
        </div>

        {/* Transport badge */}
        <div className="inline-flex items-center px-1.5 py-0.5 rounded text-[10px] uppercase font-mono tracking-wider bg-neutral-200/70 dark:bg-neutral-800 text-neutral-600 dark:text-neutral-400">
          {metrics.transport}
        </div>

        {metrics.isMock && (
          <span className="text-[10px] text-amber-600/80 dark:text-amber-400/80 font-sans">
            [simulated]
          </span>
        )}
      </div>
    </div>
  );
};
