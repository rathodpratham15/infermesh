import React, { useState, useMemo } from 'react';
import { RequestLogEntry } from '../api/types.ts';
import { buildLogExport, downloadJson, exportFileName } from '../utils/logExport.ts';
import {
  Download,
  Trash2,
  Search,
  Copy,
  Check,
  AlertTriangle,
  ChevronDown,
  ChevronRight,
  Info,
} from 'lucide-react';

interface RequestLogTableProps {
  logs: RequestLogEntry[];
  onClearLogs: () => void;
}

export const RequestLogTable: React.FC<RequestLogTableProps> = ({ logs, onClearLogs }) => {
  const [searchTerm, setSearchTerm] = useState('');
  const [statusFilter, setStatusFilter] = useState<'all' | 'success' | 'error' | 'aborted'>('all');
  const [copiedId, setCopiedId] = useState<string | null>(null);
  const [expandedRowId, setExpandedRowId] = useState<string | null>(null);

  const handleCopy = (id: string, e?: React.MouseEvent) => {
    e?.stopPropagation();
    navigator.clipboard.writeText(id);
    setCopiedId(id);
    setTimeout(() => setCopiedId(null), 2000);
  };

  // Filter logs based on search query and status filter
  const filteredLogs = useMemo(() => {
    return logs.filter((log) => {
      const q = searchTerm.toLowerCase();
      const matchesSearch =
        searchTerm === '' ||
        log.requestId.toLowerCase().includes(q) ||
        log.backendServed.toLowerCase().includes(q) ||
        (log.pinnedBackend && log.pinnedBackend.toLowerCase().includes(q)) ||
        (log.model && log.model.toLowerCase().includes(q)) ||
        (log.systemPrompt && log.systemPrompt.toLowerCase().includes(q)) ||
        (log.errorDetail && log.errorDetail.toLowerCase().includes(q));

      const matchesStatus = statusFilter === 'all' || log.status === statusFilter;
      return matchesSearch && matchesStatus;
    });
  }, [logs, searchTerm, statusFilter]);

  // Summary Metrics calculations
  const summary = useMemo(() => {
    if (logs.length === 0) return null;

    const successLogs = logs.filter((l) => l.status === 'success');
    const successRate = Math.round((successLogs.length / logs.length) * 100);

    const latencies = successLogs.map((l) => l.totalLatencyMs).filter((n) => n > 0);
    const avgLatency =
      latencies.length > 0
        ? Math.round(latencies.reduce((a, b) => a + b, 0) / latencies.length)
        : null;

    const ttfts = successLogs.map((l) => l.ttftMs).filter((n): n is number => n !== null && n > 0);
    const avgTtft =
      ttfts.length > 0 ? Math.round(ttfts.reduce((a, b) => a + b, 0) / ttfts.length) : null;

    const tpsList = successLogs
      .map((l) => l.tokensPerSec)
      .filter((n): n is number => n !== null && n > 0);
    const avgTps =
      tpsList.length > 0
        ? Number((tpsList.reduce((a, b) => a + b, 0) / tpsList.length).toFixed(1))
        : null;

    return {
      total: logs.length,
      successCount: successLogs.length,
      successRate,
      avgLatency,
      avgTtft,
      avgTps,
    };
  }, [logs]);

  // Export JSON file of the displayed log entries: exactly the rows currently shown (after the
  // search and status filters), never the unfiltered log.
  const handleExportJSON = () => {
    if (filteredLogs.length === 0) return;
    downloadJson(JSON.stringify(buildLogExport(filteredLogs), null, 2), exportFileName());
  };

  const toggleRow = (id: string) => {
    setExpandedRowId((prev) => (prev === id ? null : id));
  };

  return (
    <div className="flex-1 flex flex-col h-full bg-white dark:bg-neutral-900 overflow-hidden">
      {/* KPI Stats Strip */}
      {summary && (
        <div className="px-4 py-2.5 bg-neutral-50 dark:bg-neutral-900 border-b border-neutral-200 dark:border-neutral-800 flex flex-wrap items-center gap-6 text-xs font-mono">
          <div className="flex items-center gap-1.5">
            <span className="text-neutral-500 font-sans text-[11px]">Requests:</span>
            <span className="font-semibold text-neutral-900 dark:text-neutral-100 tabular-nums">
              {summary.total}
            </span>
          </div>

          <div className="flex items-center gap-1.5">
            <span className="text-neutral-500 font-sans text-[11px]">Success Rate:</span>
            <span
              className={`font-semibold tabular-nums ${
                summary.successRate >= 90
                  ? 'text-emerald-600 dark:text-emerald-400'
                  : 'text-amber-600 dark:text-amber-400'
              }`}
            >
              {summary.successRate}% ({summary.successCount}/{summary.total})
            </span>
          </div>

          <div className="flex items-center gap-1.5">
            <span className="text-neutral-500 font-sans text-[11px]">Avg Latency:</span>
            <span className="font-semibold text-neutral-900 dark:text-neutral-100 tabular-nums">
              {summary.avgLatency !== null ? `${summary.avgLatency}ms` : 'n/a'}
            </span>
          </div>

          <div className="flex items-center gap-1.5">
            <span className="text-neutral-500 font-sans text-[11px]">Avg TTFT:</span>
            <span className="font-semibold text-neutral-900 dark:text-neutral-100 tabular-nums">
              {summary.avgTtft !== null ? `${summary.avgTtft}ms` : 'n/a'}
            </span>
          </div>

          <div className="flex items-center gap-1.5">
            <span className="text-neutral-500 font-sans text-[11px]">Avg Tokens/s:</span>
            <span className="font-semibold text-neutral-900 dark:text-neutral-100 tabular-nums">
              {summary.avgTps !== null ? summary.avgTps : 'n/a'}
            </span>
          </div>
        </div>
      )}

      {/* Table Toolbar */}
      <div className="p-4 border-b border-neutral-200 dark:border-neutral-800 flex flex-wrap items-center justify-between gap-3">
        <div className="flex items-center gap-3 flex-1 min-w-[240px]">
          <div className="relative flex-1 max-w-sm">
            <Search className="w-3.5 h-3.5 absolute left-3 top-1/2 -translate-y-1/2 text-neutral-400" />
            <input
              type="text"
              placeholder="Search by Request ID, backend, error, model..."
              value={searchTerm}
              onChange={(e) => setSearchTerm(e.target.value)}
              className="w-full pl-9 pr-3 py-1.5 text-xs bg-neutral-50 dark:bg-neutral-800/80 border border-neutral-200 dark:border-neutral-700 rounded-md text-neutral-900 dark:text-neutral-100 placeholder:text-neutral-400 focus:outline-hidden focus:ring-1 focus:ring-neutral-400 font-sans"
            />
          </div>

          <div className="flex items-center gap-1 p-0.5 bg-neutral-100 dark:bg-neutral-800 rounded-lg text-xs">
            {(['all', 'success', 'error', 'aborted'] as const).map((s) => (
              <button
                key={s}
                onClick={() => setStatusFilter(s)}
                className={`px-2.5 py-1 capitalize rounded text-[11px] font-medium transition-colors ${
                  statusFilter === s
                    ? 'bg-white dark:bg-neutral-900 text-neutral-900 dark:text-neutral-100 shadow-xs'
                    : 'text-neutral-500 hover:text-neutral-800 dark:hover:text-neutral-200'
                }`}
              >
                {s}
              </button>
            ))}
          </div>
        </div>

        <div className="flex items-center gap-2">
          {/* Export JSON button */}
          <button
            onClick={handleExportJSON}
            disabled={filteredLogs.length === 0}
            className="inline-flex items-center gap-1.5 px-3 py-1.5 rounded-md border border-neutral-200 dark:border-neutral-700 bg-white dark:bg-neutral-800 hover:bg-neutral-50 dark:hover:bg-neutral-700 text-neutral-800 dark:text-neutral-200 text-xs font-medium disabled:opacity-40 disabled:cursor-not-allowed transition-colors"
            title="Download JSON file of all displayed request records"
          >
            <Download className="w-3.5 h-3.5" />
            <span>Export JSON ({filteredLogs.length})</span>
          </button>

          <button
            onClick={onClearLogs}
            disabled={logs.length === 0}
            className="inline-flex items-center gap-1.5 px-2.5 py-1.5 rounded-md border border-neutral-200 dark:border-neutral-700 hover:bg-rose-50 dark:hover:bg-rose-950/40 text-neutral-600 dark:text-neutral-400 hover:text-rose-600 dark:hover:text-rose-400 text-xs font-medium disabled:opacity-40 disabled:cursor-not-allowed transition-colors"
            title="Clear session request history"
          >
            <Trash2 className="w-3.5 h-3.5" />
            <span className="hidden sm:inline">Clear</span>
          </button>
        </div>
      </div>

      {/* Table Data View */}
      <div className="flex-1 overflow-auto">
        {filteredLogs.length === 0 ? (
          <div className="h-64 flex flex-col items-center justify-center text-center p-6 text-neutral-400 dark:text-neutral-500">
            <Info className="w-8 h-8 mb-2 opacity-50" />
            <p className="text-sm font-medium mb-1">No request logs match your filter</p>
            <p className="text-xs">
              {logs.length === 0
                ? 'Send a prompt in the Inference Console to benchmark gateway latency, TTFT, and routing behavior.'
                : 'Try adjusting your search query or status filter.'}
            </p>
          </div>
        ) : (
          <table className="w-full text-left text-xs font-mono border-collapse min-w-[840px]">
            <thead className="bg-neutral-50 dark:bg-neutral-800/80 sticky top-0 border-b border-neutral-200 dark:border-neutral-800 text-[11px] text-neutral-500 uppercase tracking-wider select-none z-10">
              <tr>
                <th className="py-2.5 px-3 font-medium w-8"></th>
                <th className="py-2.5 px-3 font-medium">Timestamp</th>
                <th className="py-2.5 px-3 font-medium">Transport</th>
                <th className="py-2.5 px-3 font-medium">Backend</th>
                <th className="py-2.5 px-3 font-medium">Status / Error</th>
                <th className="py-2.5 px-3 font-medium text-right">Latency</th>
                <th className="py-2.5 px-3 font-medium text-right">TTFT</th>
                <th className="py-2.5 px-3 font-medium text-right">Tokens/s</th>
                <th className="py-2.5 px-3 font-medium text-right">Output</th>
                <th className="py-2.5 px-3 font-medium">Request ID</th>
              </tr>
            </thead>
            <tbody className="divide-y divide-neutral-100 dark:divide-neutral-800/60 font-mono">
              {filteredLogs.map((log) => {
                const dateObj = new Date(log.timestamp);
                const timeStr = dateObj.toLocaleTimeString();
                const isSuccess = log.status === 'success';
                const isAborted = log.status === 'aborted';
                const hasMismatch = Boolean(log.pinningMismatchWarning);
                const isExpanded = expandedRowId === log.id;

                return (
                  <React.Fragment key={log.id}>
                    <tr
                      onClick={() => toggleRow(log.id)}
                      className="hover:bg-neutral-50/80 dark:hover:bg-neutral-800/40 cursor-pointer transition-colors"
                    >
                      {/* Expand Toggle */}
                      <td className="py-2.5 pl-3 pr-1 text-neutral-400">
                        {isExpanded ? (
                          <ChevronDown className="w-3.5 h-3.5" />
                        ) : (
                          <ChevronRight className="w-3.5 h-3.5" />
                        )}
                      </td>

                      {/* Timestamp */}
                      <td
                        className="py-2.5 px-3 text-neutral-500 whitespace-nowrap tabular-nums"
                        title={dateObj.toISOString()}
                      >
                        {timeStr}
                      </td>

                      {/* Transport Used */}
                      <td className="py-2.5 px-3 whitespace-nowrap">
                        <span className="px-1.5 py-0.5 rounded text-[10px] bg-neutral-100 dark:bg-neutral-800 text-neutral-700 dark:text-neutral-300 uppercase">
                          {log.transport}
                        </span>
                      </td>

                      {/* Backend Served */}
                      <td className="py-2.5 px-3 whitespace-nowrap">
                        <div className="flex items-center gap-1">
                          <span className="font-semibold text-neutral-800 dark:text-neutral-200">
                            {log.backendServed}
                          </span>
                          {log.pinnedBackend && (
                            <span className="text-[10px] text-neutral-400">
                              (pin: {log.pinnedBackend})
                            </span>
                          )}
                          {hasMismatch && (
                            <span
                              className="text-amber-500 cursor-help"
                              title={log.pinningMismatchWarning}
                            >
                              <AlertTriangle className="w-3.5 h-3.5 inline" />
                            </span>
                          )}
                        </div>
                      </td>

                      {/* Status / Error */}
                      <td className="py-2.5 px-3">
                        {isSuccess ? (
                          <span className="text-emerald-600 dark:text-emerald-400 font-semibold text-[11px]">
                            200 OK
                          </span>
                        ) : isAborted ? (
                          <span className="text-amber-600 dark:text-amber-400 text-[11px]">
                            Aborted
                          </span>
                        ) : (
                          <div
                            className="text-rose-600 dark:text-rose-400 text-[11px] truncate max-w-xs"
                            title={log.errorDetail}
                          >
                            {log.errorDetail || 'Error'}
                            {log.retryAfter && (
                              <span className="ml-1 text-[10px] text-amber-600">
                                (Retry: {log.retryAfter}s)
                              </span>
                            )}
                          </div>
                        )}
                      </td>

                      {/* Latency */}
                      <td className="py-2.5 px-3 text-right tabular-nums text-neutral-800 dark:text-neutral-200">
                        {log.totalLatencyMs}ms
                      </td>

                      {/* TTFT */}
                      <td className="py-2.5 px-3 text-right tabular-nums text-neutral-800 dark:text-neutral-200">
                        {log.ttftMs !== null ? (
                          `${log.ttftMs}ms`
                        ) : (
                          <span className="text-neutral-400 italic">n/a</span>
                        )}
                      </td>

                      {/* Tokens/s */}
                      <td className="py-2.5 px-3 text-right tabular-nums text-neutral-800 dark:text-neutral-200">
                        {log.tokensPerSec !== null ? (
                          log.tokensPerSec
                        ) : (
                          <span
                            className="text-neutral-400 italic cursor-help"
                            title={log.tokensPerSecReason || 'usage not reported'}
                          >
                            n/a
                          </span>
                        )}
                      </td>

                      {/* Output Tokens */}
                      <td className="py-2.5 px-3 text-right tabular-nums text-neutral-800 dark:text-neutral-200">
                        {log.completionTokens !== null ? (
                          `${log.completionTokens} tok`
                        ) : (
                          <span className="text-neutral-400 italic">n/a</span>
                        )}
                      </td>

                      {/* Request ID */}
                      <td className="py-2.5 px-3 whitespace-nowrap">
                        <div className="flex items-center gap-1.5">
                          <span className="text-neutral-600 dark:text-neutral-400 truncate max-w-[120px]">
                            {log.requestId}
                          </span>
                          {log.requestId && log.requestId !== 'n/a' && (
                            <button
                              onClick={(e) => handleCopy(log.requestId, e)}
                              className="p-1 text-neutral-400 hover:text-neutral-700 dark:hover:text-neutral-200 rounded"
                              title="Copy ID"
                            >
                              {copiedId === log.requestId ? (
                                <Check className="w-3 h-3 text-emerald-600" />
                              ) : (
                                <Copy className="w-3 h-3" />
                              )}
                            </button>
                          )}
                        </div>
                      </td>
                    </tr>

                    {/* Expandable Diagnostics Drawer */}
                    {isExpanded && (
                      <tr className="bg-neutral-50/70 dark:bg-neutral-850/60 border-y border-neutral-200 dark:border-neutral-800">
                        <td colSpan={10} className="p-3 pl-8 text-xs font-mono space-y-2">
                          <div className="grid grid-cols-1 md:grid-cols-2 lg:grid-cols-4 gap-3 text-[11px]">
                            <div>
                              <span className="text-neutral-400 block font-sans">Full Request ID:</span>
                              <span className="text-neutral-800 dark:text-neutral-200 select-all">
                                {log.requestId}
                              </span>
                            </div>
                            <div>
                              <span className="text-neutral-400 block font-sans">Target Model:</span>
                              <span className="text-neutral-800 dark:text-neutral-200">
                                {log.model || 'unspecified'}
                              </span>
                            </div>
                            <div>
                              <span className="text-neutral-400 block font-sans">Server TTFT:</span>
                              <span className="text-neutral-800 dark:text-neutral-200">
                                {log.serverTtftMs !== null && log.serverTtftMs !== undefined
                                  ? `${log.serverTtftMs}ms (reported by WS done frame)`
                                  : 'n/a'}
                              </span>
                            </div>
                            <div>
                              <span className="text-neutral-400 block font-sans">Token Accounting:</span>
                              <span className="text-neutral-800 dark:text-neutral-200">
                                prompt: {log.promptTokens ?? 'n/a'} · completion: {log.completionTokens ?? 'n/a'} · total: {log.totalTokens ?? 'n/a'}
                              </span>
                            </div>
                          </div>

                          {log.systemPrompt && (
                            <div className="pt-2 border-t border-neutral-200/60 dark:border-neutral-800/60">
                              <span className="text-neutral-400 block font-sans text-[11px] mb-0.5">
                                System Prompt (sent as messages[0]):
                              </span>
                              <div className="bg-white dark:bg-neutral-900 p-2 rounded border border-neutral-200 dark:border-neutral-800 whitespace-pre-wrap max-h-28 overflow-y-auto text-neutral-700 dark:text-neutral-300">
                                {log.systemPrompt}
                              </div>
                            </div>
                          )}

                          {log.promptPreview && (
                            <div className="pt-1">
                              <span className="text-neutral-400 block font-sans text-[11px] mb-0.5">
                                User Prompt Preview:
                              </span>
                              <div className="bg-white dark:bg-neutral-900 p-2 rounded border border-neutral-200 dark:border-neutral-800 text-neutral-700 dark:text-neutral-300">
                                {log.promptPreview}
                              </div>
                            </div>
                          )}

                          {log.errorDetail && (
                            <div className="pt-1 text-rose-600 dark:text-rose-400">
                              <span className="font-semibold block font-sans text-[11px]">
                                Detailed Error:
                              </span>
                              <div className="p-2 bg-rose-50 dark:bg-rose-950/40 rounded border border-rose-200 dark:border-rose-900/60">
                                {log.errorDetail}
                              </div>
                            </div>
                          )}
                        </td>
                      </tr>
                    )}
                  </React.Fragment>
                );
              })}
            </tbody>
          </table>
        )}
      </div>

      {/* Footer Info */}
      <div className="p-2.5 border-t border-neutral-200 dark:border-neutral-800 text-[11px] text-neutral-400 flex items-center justify-between font-mono bg-neutral-50/50 dark:bg-neutral-900">
        <span>Session logs: {logs.length} requests recorded</span>
        <span>Click any row to inspect diagnostics & system prompt</span>
      </div>
    </div>
  );
};
