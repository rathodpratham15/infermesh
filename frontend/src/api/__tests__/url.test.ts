import { describe, expect, it } from 'vitest';
import { resolveApiUrl } from '../client.ts';

const DEFAULT = 'http://localhost:8080';

describe('resolveApiUrl', () => {
  it('uses same-origin paths in dev with the default gateway (the Vite proxy forwards them)', () => {
    expect(resolveApiUrl(DEFAULT, '/v1/models', true, DEFAULT)).toBe('/v1/models');
    expect(resolveApiUrl(DEFAULT + '/', '/v1/models', true, DEFAULT)).toBe('/v1/models');
  });
  it('goes straight to the configured gateway when it is not the dev default', () => {
    expect(resolveApiUrl('https://gw.example.com', '/readyz', true, DEFAULT)).toBe('https://gw.example.com/readyz');
    expect(resolveApiUrl('https://gw.example.com/', '/readyz', false, DEFAULT)).toBe('https://gw.example.com/readyz');
  });
  it('goes to the gateway URL in a production build even if it equals the dev default (needs CORS)', () => {
    expect(resolveApiUrl(DEFAULT, '/v1/backends', false, DEFAULT)).toBe('http://localhost:8080/v1/backends');
  });
  it('treats an empty gateway URL as same origin', () => {
    expect(resolveApiUrl('', '/v1/chat/completions', false, DEFAULT)).toBe('/v1/chat/completions');
  });
});
