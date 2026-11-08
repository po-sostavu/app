import { Anchor, AppShell, Group, Stack, Text } from '@mantine/core';
import { IconScan, IconUserCircle } from '@tabler/icons-react';
import { Link, NavLink, Outlet } from 'react-router';

import { InstallAppButton } from '@/features/install-app';

import { PwaStatus } from '../pwa/PwaStatus';
import styles from './AppLayout.module.css';

const navigation = [
  { to: '/', label: 'Анализ', icon: IconScan },
  { to: '/profile', label: 'Профиль', icon: IconUserCircle },
];

export function AppLayout() {
  return (
    <AppShell
      header={{ height: 'calc(56px + env(safe-area-inset-top, 0px))' }}
      footer={{ height: 'calc(64px + env(safe-area-inset-bottom, 0px))' }}
      padding={{ base: 20, sm: 40 }}
      classNames={{ main: styles.main, header: styles.header, footer: styles.navigation }}
    >
      <a href="#main-content" className={styles.skipLink}>
        Перейти к содержимому
      </a>
      <AppShell.Header>
        <Group
          className={`${styles.container} ${styles.toolbar}`}
          justify="space-between"
          wrap="nowrap"
          gap="sm"
        >
          <Anchor component={Link} to="/" className={styles.brand} underline="never" c="inherit">
            <img src="/logo.png" alt="" width="32" height="32" />
            <Text component="span" size="lg" fw={600}>
              ПоСоставу
            </Text>
          </Anchor>
          <InstallAppButton />
        </Group>
      </AppShell.Header>
      <AppShell.Main id="main-content" tabIndex={-1}>
        <Stack className={styles.content} gap="lg">
          <PwaStatus />
          <Outlet />
        </Stack>
      </AppShell.Main>
      <AppShell.Footer>
        <nav aria-label="Основные разделы" className={`${styles.container} ${styles.bottomNav}`}>
          {navigation.map(({ to, label, icon: Icon }) => (
            <NavLink key={to} to={to} end className={styles.navLink}>
              <Icon size={24} stroke={1.5} aria-hidden="true" />
              <span>{label}</span>
            </NavLink>
          ))}
        </nav>
      </AppShell.Footer>
    </AppShell>
  );
}
