import type { StorybookConfig } from '@storybook/react-vite'

const config: StorybookConfig = {
  stories: ['../src/components/pipeline/**/*.stories.tsx'],
  framework: {
    name: '@storybook/react-vite',
    options: {},
  },
}

export default config
