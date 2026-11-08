import { Button, Paper, Stack, Text, Title } from '@mantine/core';
import { IconPhoto, IconScan } from '@tabler/icons-react';

export function HomePage() {
  return (
    <Stack gap="lg">
      <Title order={1}>Анализ состава</Title>
      <Paper p="lg">
        <Stack gap="lg">
          <IconScan size={32} stroke={1.5} color="var(--mantine-color-eco-6)" aria-hidden="true" />
          <Title order={2} size="h3">
            Новый анализ
          </Title>
          <Text id="analysis-unavailable" c="dimmed">
            Анализ фотографий пока недоступен.
          </Text>
          <Button
            disabled
            fullWidth
            mih={48}
            leftSection={<IconPhoto size={20} />}
            aria-describedby="analysis-unavailable"
          >
            Добавить фото
          </Button>
        </Stack>
      </Paper>
    </Stack>
  );
}
