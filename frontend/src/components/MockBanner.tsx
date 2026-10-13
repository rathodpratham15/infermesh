import React from 'react';
import { AlertCircle, Sliders } from 'lucide-react';

interface MockBannerProps {
  onOpenSettings?: () => void;
  onDisableMock?: () => void;
}

export const MockBanner: React.FC<MockBannerProps> = ({ onOpenSettings }) => {
  return (
    <div
      role="banner"
      className="w-full bg-amber-500/10 border-b border-amber-500/20 px-4 py-2 text-xs text-amber-800 dark:text-amber-300 flex items-center justify-between gap-4 font-mono select-none"
    >
      <div className="flex items-center gap-2">
        <AlertCircle className="w-4 h-4 text-amber-600 dark:text-amber-400 shrink-0" />
        <span className="font-semibold tracking-wide">
          MOCK MODE: simulated data, not a real gateway
        </span>
        <span className="hidden md:inline text-amber-700/80 dark:text-amber-400/70 font-sans">
          (Simulated streaming, backends and usage so the console works with no server. Connect to a real gateway with <code className="px-1.5 py-0.5 rounded bg-amber-500/10 text-amber-900 dark:text-amber-200 font-mono">VITE_MOCK=false</code>)
        </span>
      </div>

      {onOpenSettings && (
        <button
          onClick={onOpenSettings}
          className="inline-flex items-center gap-1.5 px-2.5 py-1 text-xs font-sans font-medium text-amber-900 dark:text-amber-200 bg-amber-500/15 hover:bg-amber-500/25 rounded transition-colors shrink-0"
          title="Configure Gateway Connection"
        >
          <Sliders className="w-3.5 h-3.5" />
          <span>Config</span>
        </button>
      )}
    </div>
  );
};
