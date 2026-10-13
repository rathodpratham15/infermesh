import { useState, useEffect } from 'react';
import { TransportType } from '../api/types.ts';

const SETTINGS_KEY = 'infermesh_console_settings_v1';

export interface ConsoleSettings {
  gatewayUrl: string;
  isMock: boolean;
  transport: TransportType;
  pinnedBackend: string;
  model: string;
  maxTokens: number;
  temperature: number;
  systemPrompt: string;
  forceMockError: 'none' | '429' | '503' | 'midstream' | '400_pin';
}

const DEFAULT_SETTINGS: ConsoleSettings = {
  // ?? (not ||) so an explicitly empty VITE_GATEWAY_URL means "same origin".
  gatewayUrl: (import.meta.env.VITE_GATEWAY_URL ?? 'http://localhost:8080') as string,
  // Mock mode is on unless VITE_MOCK is explicitly 'false', so the console works with no server
  isMock: import.meta.env.VITE_MOCK !== 'false',
  transport: 'sse',
  pinnedBackend: '',
  // A real gateway rejects or ignores a made-up model name, so only the mock gets a default;
  // App picks the first /v1/models entry when connected to a real gateway.
  model: import.meta.env.VITE_MOCK === 'false' ? '' : 'mock-llama-3.1-8b-instruct',
  maxTokens: 512,
  temperature: 0.7,
  systemPrompt: '',
  forceMockError: 'none',
};

export function useInferMeshSettings() {
  const [settings, setSettings] = useState<ConsoleSettings>(() => {
    try {
      const stored = localStorage.getItem(SETTINGS_KEY);
      if (stored) {
        const parsed = JSON.parse(stored);
        return {
          ...DEFAULT_SETTINGS,
          ...parsed,
          // Always respect env var if set to force
          isMock:
            import.meta.env.VITE_MOCK === 'false'
              ? false
              : parsed.isMock ?? DEFAULT_SETTINGS.isMock,
        };
      }
    } catch (e) {
      console.warn('Could not read settings from localStorage', e);
    }
    return DEFAULT_SETTINGS;
  });

  useEffect(() => {
    try {
      localStorage.setItem(SETTINGS_KEY, JSON.stringify(settings));
    } catch (e) {
      console.warn('Could not save settings to localStorage', e);
    }
  }, [settings]);

  const updateSetting = <K extends keyof ConsoleSettings>(key: K, value: ConsoleSettings[K]) => {
    setSettings((prev) => ({ ...prev, [key]: value }));
  };

  const resetSettings = () => {
    setSettings(DEFAULT_SETTINGS);
  };

  return {
    settings,
    updateSetting,
    resetSettings,
  };
}
