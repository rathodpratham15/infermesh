import React, { useState } from 'react';
import { ConsoleSettings } from '../hooks/useInferMeshSettings.ts';
import { X, Check, Globe, Shield, Terminal } from 'lucide-react';

interface SettingsModalProps {
  isOpen: boolean;
  onClose: () => void;
  settings: ConsoleSettings;
  updateSetting: <K extends keyof ConsoleSettings>(key: K, value: ConsoleSettings[K]) => void;
  onReset: () => void;
}

export const SettingsModal: React.FC<SettingsModalProps> = ({
  isOpen,
  onClose,
  settings,
  updateSetting,
  onReset,
}) => {
  if (!isOpen) return null;

  return (
    <div className="fixed inset-0 z-50 flex items-center justify-center p-4 bg-neutral-900/50 backdrop-blur-xs">
      <div className="w-full max-w-lg bg-white dark:bg-neutral-900 rounded-xl border border-neutral-200 dark:border-neutral-800 shadow-xl overflow-hidden font-sans">
        {/* Header */}
        <div className="flex items-center justify-between p-4 border-b border-neutral-200 dark:border-neutral-800">
          <div className="flex items-center gap-2">
            <Globe className="w-4 h-4 text-neutral-600 dark:text-neutral-400" />
            <h3 className="text-sm font-semibold text-neutral-900 dark:text-neutral-100">
              Gateway Connection Configuration
            </h3>
          </div>
          <button
            onClick={onClose}
            className="p-1 text-neutral-400 hover:text-neutral-700 dark:hover:text-neutral-200 rounded"
          >
            <X className="w-4 h-4" />
          </button>
        </div>

        {/* Body */}
        <div className="p-4 space-y-5 text-xs text-neutral-700 dark:text-neutral-300">
          {/* Gateway Base URL */}
          <div className="space-y-1.5">
            <label className="font-medium text-neutral-900 dark:text-neutral-100">
              Gateway Base URL (VITE_GATEWAY_URL)
            </label>
            <input
              type="text"
              value={settings.gatewayUrl}
              onChange={(e) => updateSetting('gatewayUrl', e.target.value)}
              placeholder="http://localhost:8080"
              className="w-full px-3 py-2 bg-neutral-50 dark:bg-neutral-800 border border-neutral-200 dark:border-neutral-700 rounded-md text-neutral-900 dark:text-neutral-100 font-mono text-xs focus:outline-hidden focus:ring-1 focus:ring-neutral-400"
            />
            <p className="text-[11px] text-neutral-500 leading-normal">
              HTTP paths (<code className="font-mono">/v1</code>, <code className="font-mono">/readyz</code>) are proxied by the dev server to avoid CORS issues. WebSocket connects directly using this base URL.
            </p>
          </div>

          {/* Mock Mode Switch */}
          <div className="p-3 rounded-lg border border-neutral-200 dark:border-neutral-800 bg-neutral-50/50 dark:bg-neutral-800/40 flex items-center justify-between gap-4">
            <div>
              <div className="font-semibold text-neutral-900 dark:text-neutral-100">
                Mock Mode (Simulated Gateway)
              </div>
              <p className="text-[11px] text-neutral-500 mt-0.5">
                Simulated data, enabled by default so the console works with no server. Disable to connect to a real local or remote InferMesh gateway.
              </p>
            </div>
            <button
              onClick={() => updateSetting('isMock', !settings.isMock)}
              className={`relative inline-flex h-6 w-11 shrink-0 cursor-pointer rounded-full border-2 border-transparent transition-colors duration-200 ease-in-out focus:outline-hidden ${
                settings.isMock ? 'bg-amber-500' : 'bg-neutral-300 dark:bg-neutral-700'
              }`}
            >
              <span
                className={`pointer-events-none inline-block h-5 w-5 transform rounded-full bg-white shadow-sm ring-0 transition duration-200 ease-in-out ${
                  settings.isMock ? 'translate-x-5' : 'translate-x-0'
                }`}
              />
            </button>
          </div>

          {/* Gateway Environment & CORS Info */}
          <div className="p-3 rounded-lg border border-neutral-200 dark:border-neutral-800 bg-neutral-50 dark:bg-neutral-800/20 space-y-2">
            <div className="flex items-center gap-1.5 font-semibold text-neutral-800 dark:text-neutral-200">
              <Terminal className="w-3.5 h-3.5" />
              <span>Real Gateway Environment Checklist</span>
            </div>
            <p className="text-[11px] text-neutral-500 leading-relaxed font-sans">
              When running locally against InferMesh, ensure your gateway is configured with:
            </p>
            <div className="font-mono text-[10px] space-y-1 bg-white dark:bg-neutral-950 p-2 rounded border border-neutral-200 dark:border-neutral-800 text-neutral-700 dark:text-neutral-300">
              <div>CORS_ALLOWED_ORIGINS="http://localhost:3000,http://localhost:5173"</div>
              <div>WS_ALLOWED_ORIGINS="localhost:3000,localhost:5173"</div>
            </div>
          </div>
        </div>

        {/* Footer */}
        <div className="p-3 border-t border-neutral-200 dark:border-neutral-800 bg-neutral-50 dark:bg-neutral-900 flex justify-between items-center">
          <button
            onClick={onReset}
            className="text-xs text-neutral-500 hover:text-neutral-800 dark:hover:text-neutral-200 underline"
          >
            Reset to Defaults
          </button>
          <button
            onClick={onClose}
            className="px-4 py-1.5 bg-neutral-900 dark:bg-neutral-100 text-white dark:text-neutral-900 rounded-md text-xs font-medium hover:bg-neutral-800 dark:hover:bg-neutral-200 transition-colors"
          >
            Done
          </button>
        </div>
      </div>
    </div>
  );
};
