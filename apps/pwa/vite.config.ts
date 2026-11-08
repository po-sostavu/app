import react from '@vitejs/plugin-react';
import { URL, fileURLToPath } from 'node:url';
import { defineConfig, loadEnv } from 'vite';
import { VitePWA } from 'vite-plugin-pwa';

import { parseEnvironment } from './src/shared/config/parse-env.ts';

export default defineConfig(({ mode, command }) => {
  const env = parseEnvironment(loadEnv(mode, process.cwd(), 'VITE_'), command === 'build');
  const apiOrigin = env.apiBaseUrl.startsWith('/') ? '' : new URL(env.apiBaseUrl).origin;
  const contentSecurityPolicy = [
    "default-src 'self'",
    "script-src 'self'",
    // Mantine generates style elements and inline style attributes.
    "style-src 'self' 'unsafe-inline'",
    "img-src 'self' data:",
    "font-src 'self'",
    `connect-src 'self'${apiOrigin ? ` ${apiOrigin}` : ''}`,
    "worker-src 'self'",
    "manifest-src 'self'",
    "object-src 'none'",
    "base-uri 'none'",
    "form-action 'self'",
    "frame-src 'none'",
  ].join('; ');

  return {
    build: { assetsInlineLimit: 0 },
    envPrefix: [],
    define: { 'import.meta.env.VITE_API_BASE_URL': JSON.stringify(env.apiBaseUrl) },
    resolve: { alias: { '@': fileURLToPath(new URL('./src', import.meta.url)) } },
    plugins: [
      react(),
      {
        name: 'production-content-security-policy',
        apply: 'build',
        transformIndexHtml: {
          order: 'post',
          handler: () => [
            {
              tag: 'meta',
              attrs: { 'http-equiv': 'Content-Security-Policy', content: contentSecurityPolicy },
              injectTo: 'head-prepend',
            },
          ],
        },
      },
      VitePWA({
        registerType: 'prompt',
        injectRegister: null,
        includeAssets: ['logo.png', 'favicon.ico', 'apple-touch-icon-180x180.png'],
        manifest: {
          id: '/',
          name: 'ПоСоставу',
          short_name: 'ПоСоставу',
          description: 'Понятный разбор состава и выбор шампуня с учётом ваших предпочтений.',
          lang: 'ru',
          start_url: '/',
          scope: '/',
          display: 'standalone',
          theme_color: '#386B55',
          background_color: '#F7F9F5',
          related_applications: [{ platform: 'webapp', url: '/manifest.webmanifest' }],
          icons: [
            { src: '/pwa-192x192.png', sizes: '192x192', type: 'image/png', purpose: 'any' },
            { src: '/pwa-512x512.png', sizes: '512x512', type: 'image/png', purpose: 'any' },
            {
              src: '/maskable-icon-512x512.png',
              sizes: '512x512',
              type: 'image/png',
              purpose: 'maskable',
            },
          ],
        },
        workbox: {
          globPatterns: ['**/*.{js,css,html,woff2}'],
          cleanupOutdatedCaches: true,
          navigateFallback: 'index.html',
          navigateFallbackAllowlist: [/^\/(?:profile\/?)?(?:\?.*)?$/],
          navigateFallbackDenylist: [/^\/api(?:\/|\?|$)/],
          runtimeCaching: [],
        },
        devOptions: { enabled: false },
      }),
    ],
  };
});
