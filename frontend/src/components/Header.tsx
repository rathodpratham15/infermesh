import React, { useState } from 'react';
import { ReadyzResponse } from '../api/types.ts';
import { Activity, Settings, RotateCcw, CheckCircle2, AlertTriangle, XCircle } from 'lucide-react';

interface HeaderProps {
  readyData: ReadyzResponse | null;
  activeTab: 'console' | 'logs';
  onTabChange: (tab: 'console' | 'logs') => void;
  requestCount: number;
  onNewChat: () => void;
  onOpenSettings: () => void;
  isMock: boolean;
}

export const Header: React.FC<HeaderProps> = ({
  readyData,
  activeTab,
  onTabChange,
  requestCount,
  onNewChat,
  onOpenSettings,
  isMock,
}) => {
  const [showReadyDetails, setShowReadyDetails] = useState(false);

  const isReady = readyData?.ready ?? false;
  const backendsMap = readyData?.backends ?? {};
  const backendCount = Object.keys(backendsMap).length;

  return (
    <header className="w-full bg-white dark:bg-neutral-900 border-b border-neutral-200 dark:border-neutral-800 transition-colors">
      <div className="max-w-7xl mx-auto px-4 h-14 flex items-center justify-between gap-6">
        {/* Zone 1: Wordmark & Gateway Health */}
        <div className="flex items-center gap-4 shrink-0">
          <div className="flex items-center gap-2.5">
            <div className="w-7 h-7 rounded-md bg-neutral-900 dark:bg-neutral-100 text-white dark:text-neutral-950 flex items-center justify-center font-bold font-mono text-sm shadow-xs">
              IM
            </div>
            <div className="flex items-center gap-2">
              <span className="font-semibold tracking-tight text-neutral-900 dark:text-neutral-100 text-base">
                InferMesh
              </span>
              <span className="text-xs text-neutral-400 dark:text-neutral-500 font-normal">
                Test Console
              </span>
            </div>
          </div>

          {/* Readyz Status indicator */}
          <div className="relative">
            <button
              onClick={() => setShowReadyDetails((prev) => !prev)}
              className="flex items-center gap-1.5 px-2 py-1 rounded text-xs font-mono border border-neutral-200 dark:border-neutral-800 hover:bg-neutral-50 dark:hover:bg-neutral-800/60 transition-colors"
              title="Click to view /readyz health diagnostics"
            >
              <span
                className={`w-2 h-2 rounded-full ${
                  isReady ? 'bg-emerald-500' : 'bg-rose-500'
                }`}
                aria-hidden="true"
              />
              <span className="text-neutral-600 dark:text-neutral-400">
                {isReady ? 'ready' : 'not ready'}
              </span>
            </button>

            {/* Ready Details Dropdown */}
            {showReadyDetails && (
              <div
                className="absolute left-0 mt-2 w-72 p-3 bg-white dark:bg-neutral-900 rounded-lg shadow-lg border border-neutral-200 dark:border-neutral-800 z-50 text-xs font-mono"
                onMouseLeave={() => setShowReadyDetails(false)}
              >
                <div className="flex items-center justify-between pb-2 border-b border-neutral-100 dark:border-neutral-800 mb-2">
                  <span className="font-semibold text-neutral-800 dark:text-neutral-200">
                    /readyz Probe
                  </span>
                  <span
                    className={`px-1.5 py-0.5 rounded text-[10px] ${
                      isReady
                        ? 'bg-emerald-500/10 text-emerald-600 dark:text-emerald-400'
                        : 'bg-rose-500/10 text-rose-600 dark:text-rose-400'
                    }`}
                  >
                    {isReady ? 'READY' : 'UNAVAILABLE'}
                  </span>
                </div>

                <div className="space-y-1.5 max-h-48 overflow-y-auto">
                  {backendCount === 0 ? (
                    <div className="text-neutral-400 text-[11px]">
                      No backend health report available.
                    </div>
                  ) : (
                    Object.entries(backendsMap).map(([name, status]) => {
                      const isOk = status === 'ok';
                      return (
                        <div
                          key={name}
                          className="flex items-center justify-between py-1 px-1.5 rounded bg-neutral-50 dark:bg-neutral-800/40"
                        >
                          <span className="text-neutral-700 dark:text-neutral-300 truncate max-w-[140px]">
                            {name}
                          </span>
                          <span
                            className={`flex items-center gap-1 text-[11px] ${
                              isOk
                                ? 'text-emerald-600 dark:text-emerald-400'
                                : 'text-rose-600 dark:text-rose-400'
                            }`}
                          >
                            {isOk ? (
                              <CheckCircle2 className="w-3 h-3" />
                            ) : (
                              <XCircle className="w-3 h-3" />
                            )}
                            {status}
                          </span>
                        </div>
                      );
                    })
                  )}
                </div>

                {isMock && (
                  <div className="mt-2.5 pt-2 border-t border-neutral-100 dark:border-neutral-800 text-[10px] text-neutral-400">
                    Simulated health check probe (Mock Mode)
                  </div>
                )}
              </div>
            )}
          </div>
        </div>

        {/* Zone 2: Navigation / Tab switcher */}
        <nav className="flex items-center gap-1 p-1 bg-neutral-100 dark:bg-neutral-800/60 rounded-lg">
          <button
            onClick={() => onTabChange('console')}
            className={`px-3 py-1.5 text-xs font-medium rounded-md transition-colors whitespace-nowrap ${
              activeTab === 'console'
                ? 'bg-white dark:bg-neutral-900 text-neutral-900 dark:text-neutral-100 shadow-xs'
                : 'text-neutral-600 dark:text-neutral-400 hover:text-neutral-900 dark:hover:text-neutral-200'
            }`}
          >
            Inference Console
          </button>
          <button
            onClick={() => onTabChange('logs')}
            className={`flex items-center gap-1.5 px-3 py-1.5 text-xs font-medium rounded-md transition-colors whitespace-nowrap ${
              activeTab === 'logs'
                ? 'bg-white dark:bg-neutral-900 text-neutral-900 dark:text-neutral-100 shadow-xs'
                : 'text-neutral-600 dark:text-neutral-400 hover:text-neutral-900 dark:hover:text-neutral-200'
            }`}
          >
            <Activity className="w-3.5 h-3.5" />
            <span>Request Log</span>
            {requestCount > 0 && (
              <span className="ml-0.5 px-1.5 py-0.2 rounded-full text-[10px] font-mono bg-neutral-200 dark:bg-neutral-700 text-neutral-800 dark:text-neutral-200">
                {requestCount}
              </span>
            )}
          </button>
        </nav>

        {/* Zone 3: Actions */}
        <div className="flex items-center gap-2 shrink-0">
          <button
            onClick={onNewChat}
            className="flex items-center gap-1.5 px-2.5 py-1.5 text-xs font-medium text-neutral-700 dark:text-neutral-300 hover:text-neutral-900 dark:hover:text-neutral-100 border border-neutral-200 dark:border-neutral-800 rounded-md hover:bg-neutral-50 dark:hover:bg-neutral-800/50 transition-colors"
            title="Clear conversation and start new session"
          >
            <RotateCcw className="w-3.5 h-3.5" />
            <span className="hidden sm:inline">New Chat</span>
          </button>

          <button
            onClick={onOpenSettings}
            className="p-1.5 text-neutral-600 dark:text-neutral-400 hover:text-neutral-900 dark:hover:text-neutral-100 border border-neutral-200 dark:border-neutral-800 rounded-md hover:bg-neutral-50 dark:hover:bg-neutral-800/50 transition-colors"
            title="Gateway Connection & Settings"
          >
            <Settings className="w-4 h-4" />
          </button>
        </div>
      </div>
    </header>
  );
};
