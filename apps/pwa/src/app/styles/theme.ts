import { Button, Paper, createTheme, rem } from '@mantine/core';
import type { CSSVariablesResolver } from '@mantine/core';

export const theme = createTheme({
  fontFamily: 'Manrope Variable, sans-serif',
  primaryColor: 'eco',
  primaryShade: 6,
  defaultRadius: 'sm',
  black: '#202C26',
  colors: {
    eco: [
      '#F7F9F5',
      '#E4EDE2',
      '#CFDFD0',
      '#AEC9B6',
      '#85AB93',
      '#608D73',
      '#386B55',
      '#2E5946',
      '#264936',
      '#202C26',
    ],
  },
  fontSizes: { xs: rem(12), sm: rem(14), md: rem(15), lg: rem(18), xl: rem(20) },
  lineHeights: { md: '1.666667' },
  headings: {
    fontFamily: 'Manrope Variable, sans-serif',
    fontWeight: '600',
    sizes: {
      h1: { fontSize: rem(40), lineHeight: '1.2' },
      h2: { fontSize: rem(30), lineHeight: '1.266667' },
      h3: { fontSize: rem(20), lineHeight: '1.4' },
    },
  },
  spacing: { xs: rem(4), sm: rem(8), md: rem(16), lg: rem(24), xl: rem(32) },
  radius: { xs: rem(4), sm: rem(8), md: rem(12), lg: rem(16), xl: rem(24) },
  other: { highlight: '#F0B66F', secondaryText: '#4D5D52' },
  components: {
    Button: Button.extend({ defaultProps: { size: 'md', radius: 'sm', mih: 48 } }),
    Paper: Paper.extend({ defaultProps: { radius: 'md', withBorder: true, shadow: 'none' } }),
  },
});

export const cssVariablesResolver: CSSVariablesResolver = () => ({
  variables: { '--app-highlight': '#F0B66F' },
  light: {
    '--mantine-color-body': '#F7F9F5',
    '--mantine-color-text': '#202C26',
    '--mantine-color-dimmed': '#4D5D52',
    '--mantine-color-default-border': '#CFDFD0',
    '--mantine-color-default': '#F7F9F5',
  },
  dark: {},
});
