import { useState, useEffect, useCallback, useRef } from 'react';
import { BackendsResponse, ModelItem, ReadyzResponse } from '../api/types.ts';
import { fetchBackends, fetchModels, fetchReadyz } from '../api/client.ts';

export function useGatewayStatus(gatewayUrl: string, isMock: boolean) {
  const [backendsData, setBackendsData] = useState<BackendsResponse | null>(null);
  const [readyData, setReadyData] = useState<ReadyzResponse | null>(null);
  const [modelsData, setModelsData] = useState<ModelItem[]>([]);
  const [modelsLoaded, setModelsLoaded] = useState(false);
  const [gatewayError, setGatewayError] = useState<string | null>(null);
  const [isPolling, setIsPolling] = useState(true);
  const [lastUpdated, setLastUpdated] = useState<number>(Date.now());

  const isMountedRef = useRef(true);

  // Poll /v1/backends and /readyz
  const pollStatus = useCallback(async () => {
    try {
      const [backendsRes, readyRes] = await Promise.allSettled([
        fetchBackends(gatewayUrl, isMock),
        fetchReadyz(gatewayUrl, isMock),
      ]);

      if (!isMountedRef.current) return;

      if (backendsRes.status === 'fulfilled') {
        setBackendsData(backendsRes.value);
        setGatewayError(null);
      } else {
        const reason = backendsRes.reason instanceof Error ? backendsRes.reason.message : 'Failed to fetch /v1/backends';
        setGatewayError(reason);
      }

      if (readyRes.status === 'fulfilled') {
        setReadyData(readyRes.value);
      } else {
        setReadyData({
          ready: false,
          backends: { gateway: 'unreachable' },
        });
      }

      setLastUpdated(Date.now());
    } catch (err) {
      if (!isMountedRef.current) return;
      const msg = err instanceof Error ? err.message : 'Gateway communication failure';
      setGatewayError(msg);
    }
  }, [gatewayUrl, isMock]);

  // Load models on mount / mode switch
  const loadModels = useCallback(async () => {
    try {
      const res = await fetchModels(gatewayUrl, isMock);
      if (!isMountedRef.current) return;
      setModelsData(res.data || []);
      setModelsLoaded(true);
    } catch {
      if (!isMountedRef.current) return;
      setModelsData([]);
      setModelsLoaded(true);
    }
  }, [gatewayUrl, isMock]);

  useEffect(() => {
    isMountedRef.current = true;
    pollStatus();
    loadModels();

    // Poll /v1/backends every 3 seconds as required
    const interval = setInterval(() => {
      if (isPolling) {
        pollStatus();
      }
    }, 3000);

    return () => {
      isMountedRef.current = false;
      clearInterval(interval);
    };
  }, [pollStatus, loadModels, isPolling]);

  return {
    backendsData,
    readyData,
    modelsData,
    modelsLoaded,
    gatewayError,
    lastUpdated,
    isPolling,
    setIsPolling,
    refreshNow: pollStatus,
  };
}
