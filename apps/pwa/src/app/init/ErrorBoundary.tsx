import { Component } from 'react';
import type { ReactNode } from 'react';

import { Button, Container, Stack, Text, Title } from '@mantine/core';

export class ErrorBoundary extends Component<{ children: ReactNode }, { failed: boolean }> {
  state = { failed: false };

  static getDerivedStateFromError() {
    return { failed: true };
  }

  render() {
    if (this.state.failed) {
      return (
        <Container size="sm" py={64}>
          <Stack align="flex-start" gap="lg">
            <Title order={1}>Не удалось открыть приложение</Title>
            <Text c="dimmed">Попробуйте перезагрузить страницу.</Text>
            <Button onClick={() => window.location.reload()}>Перезагрузить</Button>
          </Stack>
        </Container>
      );
    }
    return this.props.children;
  }
}
