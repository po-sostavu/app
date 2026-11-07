import { defineConfig, minimal2023Preset } from '@vite-pwa/assets-generator/config';

export default defineConfig({
  preset: {
    ...minimal2023Preset,
    maskable: { sizes: [512], resizeOptions: { background: '#F7F9F5', fit: 'contain' } },
    apple: { sizes: [180], resizeOptions: { background: '#F7F9F5', fit: 'contain' } },
  },
  images: ['public/logo.png'],
});
