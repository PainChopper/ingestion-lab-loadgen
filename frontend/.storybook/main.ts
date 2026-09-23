import type { StorybookConfig } from '@storybook/react-vite'

const config: StorybookConfig = {
  stories: ['../src/components/pipeline/ThrottlerActor.stories.tsx'],
  framework: {
    name: '@storybook/react-vite',
    options: {},
  },
}

export default config
