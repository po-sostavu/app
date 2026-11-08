import { describe, expect, it } from 'vitest';

import { getInstallInstructions } from './install-instructions';

describe('installation instructions', () => {
  it.each([
    ['Mozilla/5.0 (iPhone) AppleWebKit Safari', 5],
    ['Mozilla/5.0 (iPad) AppleWebKit Safari', 5],
    ['Mozilla/5.0 (Macintosh; Intel Mac OS X) AppleWebKit Safari', 5],
    ['Mozilla/5.0 (iPhone) AppleWebKit CriOS/140 Safari', 5],
  ])('offers the home screen for iOS and desktop-mode iPad: %s', (agent, touch) => {
    const instructions = getInstallInstructions(agent, touch);
    expect(instructions.steps.join(' ')).toContain('На экран Домой');
    expect(instructions.steps.join(' ')).not.toContain('Dock');
  });

  it('offers Dock only for Safari on a Mac', () => {
    expect(getInstallInstructions('Macintosh AppleWebKit Safari', 0).steps.join(' ')).toContain(
      'Dock',
    );
    expect(
      getInstallInstructions('Macintosh AppleWebKit Chrome/140 Safari', 0).steps.join(' '),
    ).not.toContain('Dock');
  });

  it('keeps an Android menu path without a native prompt', () => {
    expect(getInstallInstructions('Android Firefox/150', 5).steps.join(' ')).toContain(
      'главный экран',
    );
  });

  it('offers a browser fallback for an unknown desktop without claiming support', () => {
    const instructions = getInstallInstructions('unknown', 0);
    expect(instructions.steps.join(' ')).toContain('значок установки');
    expect(instructions.note).toContain('Если команды установки нет');
  });
});
