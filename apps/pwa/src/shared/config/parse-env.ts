export function parseEnvironment(input: Record<string, unknown>, production: boolean) {
  const value = input.VITE_API_BASE_URL ?? '/api';
  const invalid = () => {
    throw new Error(
      'VITE_API_BASE_URL: expected a root-relative path or an HTTPS URL without credentials, query or fragment; production requires a DNS hostname, local HTTP is allowed only in development.',
    );
  };

  if (typeof value !== 'string' || !value || /[\s\\?#%]/.test(value)) {
    return invalid();
  }

  if (value.startsWith('/') && !value.startsWith('//')) {
    if (value.split('/').some((segment) => segment === '.' || segment === '..')) {
      return invalid();
    }
    return { apiBaseUrl: value.replace(/\/+$/, '') || '/' };
  }

  let url: URL;
  try {
    url = new URL(value);
  } catch {
    return invalid();
  }

  const localHttp =
    !production &&
    url.protocol === 'http:' &&
    ['localhost', '127.0.0.1', '[::1]'].includes(url.hostname);

  if (
    (!localHttp && url.protocol !== 'https:') ||
    !/^https?:\/\//.test(value) ||
    /[^a-z0-9.:[\]-]/i.test(url.hostname) ||
    (production && (url.hostname.startsWith('[') || /^[\d.]+$/.test(url.hostname))) ||
    url.username ||
    url.password
  ) {
    return invalid();
  }

  return { apiBaseUrl: url.href.replace(/\/+$/, '') };
}
