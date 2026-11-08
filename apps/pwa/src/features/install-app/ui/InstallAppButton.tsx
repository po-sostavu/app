import { useState, useSyncExternalStore } from 'react';

import { Alert, Button, List, Modal, Stack, Text } from '@mantine/core';
import { IconDownload } from '@tabler/icons-react';

import { installController } from '../model/browser-controller';
import { getInstallInstructions } from '../model/install-instructions';

export function InstallAppButton() {
  const { installed, prompting } = useSyncExternalStore(
    installController.subscribe,
    installController.getSnapshot,
  );
  return installed ? null : <InstallAction prompting={prompting} />;
}

function InstallAction({ prompting }: { prompting: boolean }) {
  const [help, setHelp] = useState<'closed' | 'instructions' | 'failed'>('closed');
  const instructions = getInstallInstructions(navigator.userAgent, navigator.maxTouchPoints);

  function install() {
    void installController.requestInstall().then((result) => {
      if (result === 'instructions' || result === 'failed') setHelp(result);
    });
  }

  return (
    <>
      <Button
        size="compact-sm"
        variant="light"
        mih={44}
        px="sm"
        style={{ flexShrink: 0 }}
        leftSection={<IconDownload size={18} aria-hidden="true" />}
        loading={prompting}
        disabled={prompting}
        aria-label="Установить ПоСоставу"
        onClick={install}
      >
        Установить
      </Button>
      <Modal
        opened={help !== 'closed'}
        onClose={() => setHelp('closed')}
        title="Установить ПоСоставу"
        closeButtonProps={{ 'aria-label': 'Закрыть инструкцию' }}
        size="sm"
        centered
        xOffset={16}
      >
        <Stack gap="md">
          {help === 'failed' && (
            <Alert color="red" title="Не удалось открыть установку">
              Попробуйте установить приложение через меню браузера.
            </Alert>
          )}
          <Text>Открывайте приложение по значку на устройстве.</Text>
          <List type="ordered" spacing="sm">
            {instructions.steps.map((step) => (
              <List.Item key={step}>{step}</List.Item>
            ))}
          </List>
          <Text size="sm" c="dimmed">
            {instructions.note}
          </Text>
          <Button variant="default" onClick={() => setHelp('closed')}>
            Понятно
          </Button>
        </Stack>
      </Modal>
    </>
  );
}
