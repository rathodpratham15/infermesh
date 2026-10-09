import React, { useEffect, useRef } from 'react';
import { ChatMessage } from '../api/types.ts';
import { MetricChips } from './MetricChips.tsx';
import { AlertCircle, User, Bot, Clock } from 'lucide-react';

interface ChatThreadProps {
  messages: ChatMessage[];
  isStreaming: boolean;
  systemPrompt?: string;
  activeRequestId?: string;
  activeBackend?: string;
  onSendPreset?: (prompt: string) => void;
  onOpenSidebar?: () => void;
}

export const ChatThread: React.FC<ChatThreadProps> = ({
  messages,
  isStreaming,
  systemPrompt,
  activeRequestId,
  activeBackend,
  onSendPreset,
  onOpenSidebar,
}) => {
  const bottomRef = useRef<HTMLDivElement>(null);

  useEffect(() => {
    bottomRef.current?.scrollIntoView({ behavior: 'smooth' });
  }, [messages, isStreaming]);

  const hasSystemPrompt = Boolean(systemPrompt && systemPrompt.trim());

  if (messages.length === 0) {
    return (
      <div className="flex-1 flex flex-col items-center justify-center p-6 text-center max-w-lg mx-auto">
        <div className="w-12 h-12 rounded-xl bg-neutral-100 dark:bg-neutral-800 text-neutral-800 dark:text-neutral-200 flex items-center justify-center mb-4">
          <Bot className="w-6 h-6" />
        </div>
        <h2 className="text-base font-semibold text-neutral-900 dark:text-neutral-100 mb-1">
          InferMesh Gateway Console
        </h2>
        <p className="text-xs text-neutral-500 dark:text-neutral-400 mb-6 leading-relaxed">
          Benchmark LLM gateway routing, inspect TTFT across backends, verify tokens-per-second, and diagnose errors across SSE, WebSocket, and HTTP protocols.
        </p>

        <div className="w-full space-y-2 text-left">
          <span className="text-[11px] font-semibold uppercase tracking-wider text-neutral-400 dark:text-neutral-500">
            Quick Benchmark Prompts
          </span>
          {[
            'Explain how an inference gateway balances concurrency across GPU workers.',
            'Write a Python quicksort algorithm with brief time complexity analysis.',
            'Summarize the difference between Server-Sent Events and WebSocket for LLM streaming.',
          ].map((preset, idx) => (
            <button
              key={idx}
              onClick={() => onSendPreset?.(preset)}
              className="w-full p-2.5 rounded-lg border border-neutral-200 dark:border-neutral-800 hover:border-neutral-300 dark:hover:border-neutral-700 bg-neutral-50/50 dark:bg-neutral-800/40 hover:bg-neutral-100 dark:hover:bg-neutral-800 text-xs text-neutral-700 dark:text-neutral-300 transition-colors text-left font-sans"
            >
              {preset}
            </button>
          ))}
        </div>
      </div>
    );
  }

  return (
    <div className="flex-1 overflow-y-auto p-4 sm:p-6 space-y-6">
      {/* Active System Prompt Indicator Banner */}
      {hasSystemPrompt && (
        <div className="max-w-3xl mx-auto p-3 rounded-lg bg-neutral-100/80 dark:bg-neutral-850 border border-neutral-200 dark:border-neutral-800 text-xs text-neutral-600 dark:text-neutral-400 font-sans">
          <div className="flex items-center justify-between mb-1">
            <span className="font-semibold text-neutral-800 dark:text-neutral-200 uppercase tracking-wider text-[10px] font-mono">
              Active System Prompt (Sent as messages[0])
            </span>
            {onOpenSidebar && (
              <button
                onClick={onOpenSidebar}
                className="text-[11px] text-neutral-500 hover:text-neutral-800 dark:hover:text-neutral-200 underline"
              >
                Edit in controls
              </button>
            )}
          </div>
          <div className="whitespace-pre-wrap font-mono text-[11px] text-neutral-700 dark:text-neutral-300 max-h-24 overflow-y-auto bg-white/60 dark:bg-neutral-900/60 p-2 rounded border border-neutral-200/60 dark:border-neutral-800/60">
            {systemPrompt}
          </div>
        </div>
      )}

      {messages.map((msg) => {
        const isUser = msg.role === 'user';
        const isSystem = msg.role === 'system';

        if (isSystem) {
          return (
            <div
              key={msg.id}
              className="mx-auto max-w-2xl px-3 py-1.5 rounded bg-neutral-100 dark:bg-neutral-800/60 text-neutral-500 dark:text-neutral-400 text-xs text-center font-mono"
            >
              System prompt: {msg.content}
            </div>
          );
        }

        return (
          <div
            key={msg.id}
            className={`flex gap-3 max-w-3xl ${
              isUser ? 'ml-auto justify-end' : 'mr-auto justify-start'
            }`}
          >
            {!isUser && (
              <div className="w-7 h-7 rounded-md bg-neutral-900 dark:bg-neutral-100 text-white dark:text-neutral-900 flex items-center justify-center shrink-0 mt-0.5 shadow-xs">
                <Bot className="w-4 h-4" />
              </div>
            )}

            <div
              className={`rounded-xl px-4 py-3 text-sm leading-relaxed max-w-[85vw] sm:max-w-xl transition-all ${
                isUser
                  ? 'bg-neutral-900 dark:bg-neutral-100 text-white dark:text-neutral-900'
                  : 'bg-white dark:bg-neutral-900 border border-neutral-200 dark:border-neutral-800 text-neutral-900 dark:text-neutral-100 shadow-xs'
              }`}
            >
              {/* Message Content */}
              <div className="whitespace-pre-wrap font-sans break-words">
                {msg.content}
                {msg.isStreaming && (
                  <span className="inline-block w-1.5 h-4 ml-1 bg-neutral-800 dark:bg-neutral-200 animate-pulse align-middle" />
                )}
              </div>

              {/* Streaming active indicator */}
              {msg.isStreaming && (activeRequestId || activeBackend) && (
                <div className="mt-2 text-xs font-mono text-neutral-400 flex items-center gap-2">
                  <Clock className="w-3.5 h-3.5 animate-spin text-neutral-500" />
                  <span>Streaming from {activeBackend || 'router'}</span>
                  {activeRequestId && (
                    <span className="text-[10px] text-neutral-400">({activeRequestId})</span>
                  )}
                </div>
              )}

              {/* Stopped by the user (kept out of the content so it is never replayed to the model) */}
              {msg.aborted && (
                <div className="mt-2 text-[11px] font-mono text-amber-600 dark:text-amber-400">
                  {msg.content ? 'Stopped by user (partial response)' : 'Stopped by user before any output'}
                </div>
              )}

              {/* Inline Error display if request failed */}
              {msg.error && (
                <div className="mt-2.5 p-2.5 rounded-lg bg-rose-50 dark:bg-rose-950/40 border border-rose-200 dark:border-rose-900/60 text-xs text-rose-800 dark:text-rose-200 font-mono space-y-1">
                  <div className="flex items-start gap-1.5">
                    <AlertCircle className="w-4 h-4 text-rose-600 dark:text-rose-400 shrink-0 mt-0.5" />
                    <div className="font-sans font-medium text-rose-900 dark:text-rose-100">
                      Request Failed
                    </div>
                  </div>
                  <div className="pl-5 text-xs text-rose-700 dark:text-rose-300 break-words font-mono">
                    {msg.error}
                  </div>
                </div>
              )}

              {/* Metrics Chips under completed assistant message */}
              {msg.metrics && <MetricChips metrics={msg.metrics} />}
            </div>

            {isUser && (
              <div className="w-7 h-7 rounded-md bg-neutral-200 dark:bg-neutral-800 text-neutral-700 dark:text-neutral-300 flex items-center justify-center shrink-0 mt-0.5">
                <User className="w-4 h-4" />
              </div>
            )}
          </div>
        );
      })}

      <div ref={bottomRef} />
    </div>
  );
};
