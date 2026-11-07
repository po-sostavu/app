import '@fontsource-variable/manrope';
import '@mantine/core/styles.css';

import { StrictMode } from 'react';

import { createRoot } from 'react-dom/client';

import { App } from '@/app';

import '@/app/styles/global.css';

const root = document.getElementById('root');
if (!root) throw new Error('Application root is missing');

createRoot(root).render(
  <StrictMode>
    <App />
  </StrictMode>,
);
