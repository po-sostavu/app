import { Paper, Stack, Text, Title } from '@mantine/core';
import { IconUserCircle } from '@tabler/icons-react';

export function ProfilePage() {
  return (
    <Stack gap="lg">
      <Title order={1}>Профиль</Title>
      <Paper p="lg">
        <Stack gap="lg">
          <IconUserCircle
            size={32}
            stroke={1.5}
            color="var(--mantine-color-eco-6)"
            aria-hidden="true"
          />
          <Title order={2} size="h3">
            Предпочтения и бюджет
          </Title>
          <Text c="dimmed">Настройка профиля пока недоступна.</Text>
        </Stack>
      </Paper>
    </Stack>
  );
}
