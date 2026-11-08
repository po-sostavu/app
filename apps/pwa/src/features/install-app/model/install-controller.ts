export interface InstallEnvironment {
  events: EventTarget;
  displayMode: EventTarget & { readonly matches: boolean };
  isStandalone: () => boolean;
  origin: string;
  getInstalledRelatedApps?: () => Promise<Array<{ platform: string; url?: string; id?: string }>>;
}

export interface InstallSnapshot {
  readonly installed: boolean;
  readonly prompting: boolean;
  readonly canPrompt: boolean;
}

export type InstallResult = 'accepted' | 'dismissed' | 'instructions' | 'failed' | 'ignored';

interface InstallPromptEvent extends Event {
  prompt: () => Promise<unknown>;
  userChoice: Promise<{ outcome: 'accepted' | 'dismissed' }>;
}

export function createInstallController(environment: InstallEnvironment) {
  const listeners = new Set<() => void>();
  const usedPrompts = new WeakSet<Event>();
  const manifestUrl = `${environment.origin}/manifest.webmanifest`;
  const isStandalone = () => environment.displayMode.matches || environment.isStandalone();
  let standalone = isStandalone();
  let confirmed = false;
  let relatedInstalled = false;
  let deferredPrompt: InstallPromptEvent | undefined;
  let request: symbol | undefined;
  let revision = 0;
  let disposed = false;
  let snapshot: InstallSnapshot = {
    installed: standalone,
    prompting: false,
    canPrompt: false,
  };

  function publish() {
    if (disposed) return;
    const installed = standalone || confirmed || relatedInstalled;
    const next: InstallSnapshot = {
      installed,
      prompting: !installed && request !== undefined,
      canPrompt: !installed && request === undefined && deferredPrompt !== undefined,
    };
    if (
      next.installed === snapshot.installed &&
      next.prompting === snapshot.prompting &&
      next.canPrompt === snapshot.canPrompt
    ) {
      return;
    }
    snapshot = next;
    listeners.forEach((listener) => listener());
  }

  function clearPrompt() {
    deferredPrompt = undefined;
    request = undefined;
  }

  async function refreshRelatedApps(currentRevision: number) {
    if (!environment.getInstalledRelatedApps) return;
    try {
      const apps = await environment.getInstalledRelatedApps();
      if (disposed || currentRevision !== revision) return;
      relatedInstalled = apps.some((app) => app.platform === 'webapp' && app.url === manifestUrl);
      if (relatedInstalled) clearPrompt();
      publish();
    } catch {
      // A missing answer cannot prove that the app is absent or installed.
    }
  }

  function refresh() {
    if (disposed) return;
    const currentRevision = ++revision;
    standalone = isStandalone();
    if (standalone) clearPrompt();
    publish();
    void refreshRelatedApps(currentRevision);
  }

  function onBeforeInstallPrompt(event: Event) {
    const candidate = event as InstallPromptEvent;
    if (typeof candidate.prompt !== 'function' || usedPrompts.has(event)) return;
    event.preventDefault();
    revision++;
    standalone = isStandalone();
    if (standalone) {
      clearPrompt();
    } else {
      // A fresh browser offer also permits reinstalling after removal.
      confirmed = false;
      relatedInstalled = false;
      deferredPrompt = candidate;
    }
    publish();
  }

  function onAppInstalled() {
    revision++;
    confirmed = true;
    clearPrompt();
    publish();
  }

  environment.events.addEventListener('beforeinstallprompt', onBeforeInstallPrompt);
  environment.events.addEventListener('appinstalled', onAppInstalled);
  environment.events.addEventListener('focus', refresh);
  environment.events.addEventListener('pageshow', refresh);
  environment.displayMode.addEventListener('change', refresh);
  refresh();

  return {
    getSnapshot: () => snapshot,
    subscribe(listener: () => void) {
      if (!disposed) listeners.add(listener);
      return () => {
        listeners.delete(listener);
      };
    },
    async requestInstall(): Promise<InstallResult> {
      if (disposed || snapshot.installed || request !== undefined) return 'ignored';
      const promptEvent = deferredPrompt;
      if (!promptEvent) return 'instructions';

      const currentRequest = Symbol();
      request = currentRequest;
      deferredPrompt = undefined;
      usedPrompts.add(promptEvent);
      revision++;
      publish();

      try {
        // Invoke before the first await, while the click still has user activation.
        await promptEvent.prompt();
        if (disposed || request !== currentRequest) return 'ignored';
        const choice = await promptEvent.userChoice;
        if (disposed || request !== currentRequest) return 'ignored';
        return choice.outcome;
      } catch {
        return disposed || request !== currentRequest ? 'ignored' : 'failed';
      } finally {
        if (request === currentRequest) {
          request = undefined;
          publish();
        }
      }
    },
    dispose() {
      disposed = true;
      revision++;
      clearPrompt();
      listeners.clear();
      environment.events.removeEventListener('beforeinstallprompt', onBeforeInstallPrompt);
      environment.events.removeEventListener('appinstalled', onAppInstalled);
      environment.events.removeEventListener('focus', refresh);
      environment.events.removeEventListener('pageshow', refresh);
      environment.displayMode.removeEventListener('change', refresh);
    },
  };
}
