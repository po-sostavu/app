import { useState, useSyncExternalStore } from 'react';

import { Alert, Button, Group, Stack } from '@mantine/core';
import { IconRefresh, IconWifiOff } from '@tabler/icons-react';
import { useRegisterSW } from 'virtual:pwa-register/react';

function subscribeOnline(listener: () => void) {
  window.addEventListener('online', listener);
  window.addEventListener('offline', listener);
  return () => {
    window.removeEventListener('online', listener);
    window.removeEventListener('offline', listener);
  };
}

export function PwaStatus() {
  const online = useSyncExternalStore(
    subscribeOnline,
    () => navigator.onLine,
    () => true,
  );
  const [updateFailed, setUpdateFailed] = useState(false);
  const [updating, setUpdating] = useState(false);
  const {
    needRefresh: [needRefresh, setNeedRefresh],
    updateServiceWorker,
  } = useRegisterSW();

  if (online && !needRefresh && !updateFailed) return null;

  return (
    <Stack gap="md" aria-live="polite">
      {!online && (
        <Alert color="eco" title="Нет подключения к сети" icon={<IconWifiOff size={20} />}>
          Для отправки фото и получения анализа понадобится интернет.
        </Alert>
      )}
      {needRefresh && (
        <Alert color="eco" title="Доступна новая версия" icon={<IconRefresh size={20} />}>
          Обновление перезагрузит приложение.
          <Group mt="md" gap="sm">
            <Button
              size="sm"
              loading={updating}
              onClick={() => {
                setUpdateFailed(false);
                setUpdating(true);
                void updateServiceWorker(true)
                  .catch(() => setUpdateFailed(true))
                  .finally(() => setUpdating(false));
              }}
            >
              Обновить
            </Button>
            <Button
              size="sm"
              variant="subtle"
              disabled={updating}
              onClick={() => {
                setNeedRefresh(false);
                setUpdateFailed(false);
              }}
            >
              Позже
            </Button>
          </Group>
        </Alert>
      )}
      {updateFailed && (
        <Alert color="red" title="Не удалось обновить приложение">
          Проверьте соединение и повторите попытку.
        </Alert>
      )}
    </Stack>
  );
}
