import { Button, Stack, Text, Title } from '@mantine/core';
import { Link } from 'react-router';

export function NotFoundPage() {
  return (
    <Stack align="flex-start" gap="lg" py={64}>
      <Text c="dimmed" fw={600}>
        404
      </Text>
      <Title order={1}>Страница не найдена</Title>
      <Text c="dimmed">Возможно, адрес изменился. Вернитесь на главную страницу.</Text>
      <Button component={Link} to="/">
        На главную
      </Button>
    </Stack>
  );
}
