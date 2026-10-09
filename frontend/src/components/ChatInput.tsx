import React, { useState, useRef, useEffect } from 'react';
import { Send, Square, CornerDownLeft } from 'lucide-react';

interface ChatInputProps {
  onSend: (text: string) => void;
  onStop: () => void;
  isStreaming: boolean;
  disabled?: boolean;
}

export const ChatInput: React.FC<ChatInputProps> = ({
  onSend,
  onStop,
  isStreaming,
  disabled = false,
}) => {
  const [text, setText] = useState('');
  const textareaRef = useRef<HTMLTextAreaElement>(null);

  // Auto-resize textarea height
  useEffect(() => {
    if (textareaRef.current) {
      textareaRef.current.style.height = 'auto';
      textareaRef.current.style.height = `${Math.min(
        160,
        Math.max(48, textareaRef.current.scrollHeight)
      )}px`;
    }
  }, [text]);

  const handleKeyDown = (e: React.KeyboardEvent<HTMLTextAreaElement>) => {
    if (e.key === 'Enter' && !e.shiftKey) {
      e.preventDefault();
      handleSubmit();
    }
  };

  const handleSubmit = () => {
    if (isStreaming) {
      onStop();
      return;
    }

    const trimmed = text.trim();
    if (!trimmed || disabled) return;

    onSend(trimmed);
    setText('');
    if (textareaRef.current) {
      textareaRef.current.style.height = '48px';
    }
  };

  return (
    <div className="p-4 bg-white dark:bg-neutral-900 border-t border-neutral-200 dark:border-neutral-800">
      <div className="max-w-3xl mx-auto">
        <div className="relative flex items-end gap-2 bg-neutral-50 dark:bg-neutral-800/80 border border-neutral-200 dark:border-neutral-700 rounded-xl p-2 focus-within:border-neutral-400 dark:focus-within:border-neutral-500 focus-within:ring-1 focus-within:ring-neutral-400 transition-all shadow-xs">
          <textarea
            ref={textareaRef}
            value={text}
            onChange={(e) => setText(e.target.value)}
            onKeyDown={handleKeyDown}
            placeholder={
              isStreaming
                ? 'Generation in progress...'
                : 'Send inference prompt... (Enter to send, Shift+Enter for newline)'
            }
            disabled={disabled}
            rows={1}
            className="flex-1 max-h-40 min-h-[44px] py-2 px-2 text-sm bg-transparent border-0 resize-none focus:outline-hidden text-neutral-900 dark:text-neutral-100 placeholder:text-neutral-400 dark:placeholder:text-neutral-500 leading-normal"
          />

          <div className="flex items-center gap-1.5 pb-1 pr-1 shrink-0">
            {isStreaming ? (
              <button
                type="button"
                onClick={onStop}
                className="inline-flex items-center gap-1.5 px-3 py-2 rounded-lg bg-rose-600 hover:bg-rose-700 text-white text-xs font-medium shadow-xs transition-colors cursor-pointer"
                title="Stop streaming (aborts request on server)"
              >
                <Square className="w-3.5 h-3.5 fill-current" />
                <span>Stop</span>
              </button>
            ) : (
              <button
                type="button"
                onClick={handleSubmit}
                disabled={!text.trim() || disabled}
                className="inline-flex items-center gap-1.5 px-3 py-2 rounded-lg bg-neutral-900 dark:bg-neutral-100 text-white dark:text-neutral-900 hover:bg-neutral-800 dark:hover:bg-neutral-200 disabled:opacity-40 disabled:cursor-not-allowed text-xs font-medium shadow-xs transition-colors cursor-pointer"
                title="Send Request"
              >
                <span>Send</span>
                <Send className="w-3.5 h-3.5" />
              </button>
            )}
          </div>
        </div>

        <div className="flex items-center justify-between mt-2 px-1 text-[11px] text-neutral-400 dark:text-neutral-500 font-mono">
          <div className="flex items-center gap-1">
            <CornerDownLeft className="w-3 h-3" />
            <span>Enter sends · Shift+Enter newline</span>
          </div>
          <div>Strict latency & TTFT measurement</div>
        </div>
      </div>
    </div>
  );
};
