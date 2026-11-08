import { AppProviders } from './init/AppProviders';
import { AppRouter } from './router/AppRouter';

export function App() {
  return (
    <AppProviders>
      <AppRouter />
    </AppProviders>
  );
}
