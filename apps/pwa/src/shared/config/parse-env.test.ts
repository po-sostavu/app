import { describe, expect, it } from 'vitest';

import { parseEnvironment } from './parse-env';

describe('public environment', () => {
  it('uses the same-origin API by default and ignores unrelated settings', () => {
    expect(parseEnvironment({ PRIVATE_KEY: 'not-for-the-client' }, true)).toEqual({
      apiBaseUrl: '/api',
    });
  });

  it.each(['/api', '/api/v1/', 'https://api.example.test/v1/'])(
    'accepts and normalizes %s',
    (value) => {
      expect(parseEnvironment({ VITE_API_BASE_URL: value }, true).apiBaseUrl).toBe(
        value.replace(/\/+$/, ''),
      );
    },
  );

  it.each(['http://localhost:8080/api', 'http://127.0.0.1:8080', 'http://[::1]:8080'])(
    'allows local HTTP only in development: %s',
    (value) => {
      expect(parseEnvironment({ VITE_API_BASE_URL: value }, false).apiBaseUrl).toBe(value);
      expect(() => parseEnvironment({ VITE_API_BASE_URL: value }, true)).toThrow(
        'VITE_API_BASE_URL',
      );
    },
  );

  it.each([
    '',
    'api',
    '//example.test/api',
    '/api?key=secret',
    '/api#fragment',
    '/api/../private',
    '/api/%2e%2e/private',
    '/\\example.test',
    'javascript:alert(1)',
    'https:example.test',
    'https://user:password@example.test',
    'https://example.test?key=secret',
    'https://example.test#fragment',
    'http://example.test/api',
    ' https://example.test',
    "https://example.test';script-src'unsafe-inline'",
    'https://example.test;connect-src',
  ])('rejects invalid settings without echoing them: %s', (value) => {
    expect(() => parseEnvironment({ VITE_API_BASE_URL: value }, false)).toThrow(
      'VITE_API_BASE_URL',
    );
  });

  it('does not disclose a credential in the validation error', () => {
    let message = '';
    try {
      parseEnvironment({ VITE_API_BASE_URL: 'https://user:secret-value@example.test' }, true);
    } catch (error) {
      message = (error as Error).message;
    }
    expect(message).toContain('VITE_API_BASE_URL');
    expect(message).not.toContain('secret-value');
  });

  it.each(['https://[::1]/api', 'https://127.0.0.1/api', 'https://192.0.2.1/api'])(
    'requires a DNS hostname for production CSP: %s',
    (value) => {
      expect(() => parseEnvironment({ VITE_API_BASE_URL: value }, true)).toThrow(
        'VITE_API_BASE_URL',
      );
      expect(parseEnvironment({ VITE_API_BASE_URL: value }, false).apiBaseUrl).toBe(value);
    },
  );
});
