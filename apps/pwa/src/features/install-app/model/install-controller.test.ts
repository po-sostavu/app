import { describe, expect, it, vi } from 'vitest';

import { type InstallEnvironment, createInstallController } from './install-controller';

function deferred<T>() {
  let resolve!: (value: T) => void;
  let reject!: (error: Error) => void;
  const promise = new Promise<T>((resolvePromise, rejectPromise) => {
    resolve = resolvePromise;
    reject = rejectPromise;
  });
  return { promise, resolve, reject };
}

function setup(overrides: Partial<InstallEnvironment> = {}) {
  const events = new EventTarget();
  const displayMode = Object.assign(new EventTarget(), { matches: false });
  const environment: InstallEnvironment = {
    events,
    displayMode,
    isStandalone: () => false,
    origin: 'https://app.example',
    ...overrides,
  };
  return { events, displayMode, controller: createInstallController(environment) };
}

function offerInstall(
  events: EventTarget,
  prompt: () => Promise<unknown> = vi.fn(async () => undefined),
) {
  const choice = deferred<{ outcome: 'accepted' | 'dismissed' }>();
  const event = Object.assign(new Event('beforeinstallprompt', { cancelable: true }), {
    prompt,
    userChoice: choice.promise,
  });
  events.dispatchEvent(event);
  return { event, prompt, choice };
}

describe('PWA installation', () => {
  it('allows installing again after a fresh lookup no longer finds the app', async () => {
    const lookup = vi
      .fn()
      .mockResolvedValueOnce([
        { platform: 'webapp', url: 'https://app.example/manifest.webmanifest' },
      ])
      .mockResolvedValueOnce([]);
    const { controller, events } = setup({ getInstalledRelatedApps: lookup });
    await Promise.resolve();
    expect(controller.getSnapshot().installed).toBe(true);
    events.dispatchEvent(new Event('focus'));
    await Promise.resolve();
    expect(controller.getSnapshot().installed).toBe(false);
    expect(await controller.requestInstall()).toBe('instructions');
  });

  it('keeps manual instructions available before the browser offers installation', async () => {
    const { controller, events } = setup();
    const initial = controller.getSnapshot();
    expect(await controller.requestInstall()).toBe('instructions');
    events.dispatchEvent(new Event('focus'));
    expect(controller.getSnapshot()).toBe(initial);
    expect(initial).toEqual({ installed: false, prompting: false, canPrompt: false });
  });

  it.each(['display mode', 'iOS standalone'])(
    'hides installation when opened as %s',
    async (mode) => {
      const { controller } = setup(
        mode === 'display mode'
          ? { displayMode: Object.assign(new EventTarget(), { matches: true }) }
          : { isStandalone: () => true },
      );
      expect(controller.getSnapshot().installed).toBe(true);
      expect(await controller.requestInstall()).toBe('ignored');
    },
  );

  it('calls the native prompt immediately and blocks duplicate clicks', async () => {
    const { controller, events } = setup();
    const { prompt, choice, event } = offerInstall(events);
    expect(event.defaultPrevented).toBe(true);
    expect(controller.getSnapshot().canPrompt).toBe(true);

    const result = controller.requestInstall();
    expect(prompt).toHaveBeenCalledOnce();
    expect(controller.getSnapshot().prompting).toBe(true);
    expect(await controller.requestInstall()).toBe('ignored');
    choice.resolve({ outcome: 'dismissed' });
    expect(await result).toBe('dismissed');
    expect(controller.getSnapshot()).toEqual({
      installed: false,
      prompting: false,
      canPrompt: false,
    });
    expect(await controller.requestInstall()).toBe('instructions');

    events.dispatchEvent(event);
    expect(await controller.requestInstall()).toBe('instructions');
    expect(prompt).toHaveBeenCalledOnce();
  });

  it('requires appinstalled instead of treating acceptance as completed installation', async () => {
    const { controller, events } = setup();
    const { choice } = offerInstall(events);
    const result = controller.requestInstall();
    choice.resolve({ outcome: 'accepted' });
    expect(await result).toBe('accepted');
    expect(controller.getSnapshot().installed).toBe(false);

    events.dispatchEvent(new Event('appinstalled'));
    expect(controller.getSnapshot()).toEqual({
      installed: true,
      prompting: false,
      canPrompt: false,
    });
  });

  it('accepts a fresh browser offer after a dismissal or removal', async () => {
    const { controller, events } = setup();
    const first = offerInstall(events);
    const dismissed = controller.requestInstall();
    first.choice.resolve({ outcome: 'dismissed' });
    await dismissed;
    const second = offerInstall(events);
    const accepted = controller.requestInstall();
    second.choice.resolve({ outcome: 'accepted' });
    expect(await accepted).toBe('accepted');
    events.dispatchEvent(new Event('appinstalled'));

    const third = offerInstall(events);
    expect(controller.getSnapshot().installed).toBe(false);
    const reinstalled = controller.requestInstall();
    third.choice.resolve({ outcome: 'accepted' });
    expect(await reinstalled).toBe('accepted');
    expect(third.prompt).toHaveBeenCalledOnce();
  });

  it('returns to instructions after prompt failure without losing a new offer', async () => {
    const { controller, events } = setup();
    const failedPrompt = deferred<void>();
    offerInstall(
      events,
      vi.fn(() => failedPrompt.promise),
    );
    const result = controller.requestInstall();
    failedPrompt.reject(new Error('Browser rejected the prompt'));
    expect(await result).toBe('failed');
    expect(await controller.requestInstall()).toBe('instructions');

    const next = offerInstall(events);
    const retry = controller.requestInstall();
    next.choice.resolve({ outcome: 'dismissed' });
    expect(await retry).toBe('dismissed');
  });

  it('does not undo appinstalled when a pending prompt fails', async () => {
    const { controller, events } = setup();
    const failedPrompt = deferred<void>();
    offerInstall(
      events,
      vi.fn(() => failedPrompt.promise),
    );
    const result = controller.requestInstall();
    events.dispatchEvent(new Event('appinstalled'));
    failedPrompt.reject(new Error('Late browser failure'));
    expect(await result).toBe('ignored');
    expect(controller.getSnapshot().installed).toBe(true);
    expect(controller.getSnapshot().prompting).toBe(false);
  });

  it('keeps a newer offer when the previous prompt fails late', async () => {
    const { controller, events } = setup();
    const firstPrompt = deferred<void>();
    offerInstall(events, () => firstPrompt.promise);
    const firstResult = controller.requestInstall();
    const second = offerInstall(events);
    firstPrompt.reject(new Error('First prompt failed'));
    expect(await firstResult).toBe('failed');
    expect(controller.getSnapshot().canPrompt).toBe(true);
    const secondResult = controller.requestInstall();
    second.choice.resolve({ outcome: 'accepted' });
    expect(await secondResult).toBe('accepted');
    expect(second.prompt).toHaveBeenCalledOnce();
  });

  it('refreshes display mode and iOS standalone on browser lifecycle events', () => {
    let iosStandalone = false;
    const { controller, events, displayMode } = setup({ isStandalone: () => iosStandalone });
    displayMode.matches = true;
    displayMode.dispatchEvent(new Event('change'));
    expect(controller.getSnapshot().installed).toBe(true);
    displayMode.matches = false;
    events.dispatchEvent(new Event('pageshow'));
    expect(controller.getSnapshot().installed).toBe(false);
    iosStandalone = true;
    events.dispatchEvent(new Event('focus'));
    expect(controller.getSnapshot().installed).toBe(true);
  });

  it.each([
    { platform: 'webapp', url: 'https://app.example/manifest.webmanifest', matches: true },
    { platform: 'webapp', url: 'https://other.example/manifest.webmanifest', matches: false },
    { platform: 'webapp', url: 'https://app.example/other.webmanifest', matches: false },
    { platform: 'play', url: 'https://app.example/manifest.webmanifest', matches: false },
    { platform: 'webapp', id: 'https://app.example/', matches: false },
  ])('accepts only the exact own webapp manifest: %j', async ({ matches, ...app }) => {
    const { controller } = setup({ getInstalledRelatedApps: async () => [app] });
    await Promise.resolve();
    expect(controller.getSnapshot().installed).toBe(matches);
  });

  it('does not block instructions while the optional installed-app check is pending', async () => {
    const lookup = deferred<[]>();
    const { controller } = setup({ getInstalledRelatedApps: () => lookup.promise });
    expect(await controller.requestInstall()).toBe('instructions');
    lookup.reject(new Error('Not available'));
    await Promise.resolve();
    expect(await controller.requestInstall()).toBe('instructions');
  });

  it('ignores a stale positive lookup after a new installation offer', async () => {
    const lookup = deferred<Array<{ platform: string; url: string }>>();
    const { controller, events } = setup({ getInstalledRelatedApps: () => lookup.promise });
    offerInstall(events);
    lookup.resolve([{ platform: 'webapp', url: 'https://app.example/manifest.webmanifest' }]);
    await Promise.resolve();
    expect(controller.getSnapshot().installed).toBe(false);
    expect(controller.getSnapshot().canPrompt).toBe(true);
  });

  it('preserves appinstalled when an older lookup returns no matches', async () => {
    const lookup = deferred<[]>();
    const { controller, events } = setup({ getInstalledRelatedApps: () => lookup.promise });
    events.dispatchEvent(new Event('appinstalled'));
    lookup.resolve([]);
    await Promise.resolve();
    expect(controller.getSnapshot().installed).toBe(true);
  });

  it('ignores the older of two concurrent installed-app lookups', async () => {
    const first = deferred<Array<{ platform: string; url: string }>>();
    const second = deferred<[]>();
    const lookup = vi.fn().mockReturnValueOnce(first.promise).mockReturnValueOnce(second.promise);
    const { controller, events } = setup({ getInstalledRelatedApps: lookup });
    events.dispatchEvent(new Event('focus'));
    second.resolve([]);
    first.resolve([{ platform: 'webapp', url: 'https://app.example/manifest.webmanifest' }]);
    await Promise.resolve();
    expect(controller.getSnapshot().installed).toBe(false);
  });

  it('removes listeners and ignores asynchronous results after disposal', async () => {
    const lookup = deferred<Array<{ platform: string; url: string }>>();
    const { controller, events, displayMode } = setup({
      getInstalledRelatedApps: () => lookup.promise,
    });
    const listener = vi.fn();
    controller.subscribe(listener);
    const initial = controller.getSnapshot();
    controller.dispose();
    const offered = offerInstall(events);
    events.dispatchEvent(new Event('appinstalled'));
    displayMode.matches = true;
    displayMode.dispatchEvent(new Event('change'));
    lookup.resolve([{ platform: 'webapp', url: 'https://app.example/manifest.webmanifest' }]);
    await Promise.resolve();
    expect(offered.event.defaultPrevented).toBe(false);
    expect(controller.getSnapshot()).toBe(initial);
    expect(listener).not.toHaveBeenCalled();
    expect(await controller.requestInstall()).toBe('ignored');
  });
});
