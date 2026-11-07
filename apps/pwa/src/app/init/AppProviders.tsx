import { useState } from 'react';
import type { ReactNode } from 'react';

import { MantineProvider } from '@mantine/core';
import { QueryClient, QueryClientProvider } from '@tanstack/react-query';

import { cssVariablesResolver, theme } from '../styles/theme';
import { ErrorBoundary } from './ErrorBoundary';

export function AppProviders({ children }: { children: ReactNode }) {
  const [queryClient] = useState(
    () => new QueryClient({ defaultOptions: { mutations: { retry: false } } }),
  );

  return (
    <MantineProvider
      theme={theme}
      cssVariablesResolver={cssVariablesResolver}
      forceColorScheme="light"
    >
      <ErrorBoundary>
        <QueryClientProvider client={queryClient}>{children}</QueryClientProvider>
      </ErrorBoundary>
    </MantineProvider>
  );
}
