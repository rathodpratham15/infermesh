import tailwindcss from '@tailwindcss/vite';
import react from '@vitejs/plugin-react';
import {defineConfig, loadEnv} from 'vite';

// In `npm run dev`, same-origin API calls (/v1, /readyz, /healthz) are proxied to the gateway, so the browser
// needs no CORS for HTTP. The target is VITE_GATEWAY_URL (from the shell or a .env file). The WebSocket always
// connects to the gateway directly, so the gateway must allow this origin with WS_ALLOWED_ORIGINS.
export default defineConfig(({mode}) => {
  const env = loadEnv(mode, process.cwd(), '');
  const gateway = env.VITE_GATEWAY_URL ?? 'http://localhost:8080';
  const proxy = gateway
    ? Object.fromEntries(['/v1', '/healthz', '/readyz'].map((path) => [path, {target: gateway, changeOrigin: true}]))
    : undefined;

  return {
    plugins: [react(), tailwindcss()],
    server: {proxy},
  };
});
