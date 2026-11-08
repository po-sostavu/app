import { createInstallController } from './install-controller';

const browserNavigator = navigator as Navigator & {
  standalone?: boolean;
  getInstalledRelatedApps?: () => Promise<{ platform: string; url?: string; id?: string }[]>;
};

export const installController = createInstallController({
  events: window,
  displayMode: window.matchMedia(
    '(display-mode: standalone), (display-mode: minimal-ui), (display-mode: window-controls-overlay)',
  ),
  isStandalone: () => browserNavigator.standalone === true,
  origin: window.location.origin,
  getInstalledRelatedApps: browserNavigator.getInstalledRelatedApps?.bind(browserNavigator),
});

if (import.meta.hot) {
  import.meta.hot.dispose(() => installController.dispose());
}
