import { defineConfig } from 'astro/config'
import starlight from '@astrojs/starlight'

export default defineConfig({
  site: 'https://lukaskoebe.github.io',
  base: '/sandbox-studio',
  trailingSlash: 'always',
  // Screenshots are already compressed JPEGs; keep builds free of native image tools.
  image: { service: { entrypoint: 'astro/assets/services/noop' } },
  integrations: [
    starlight({
      title: 'Sandbox Studio',
      description:
        'Guides for running coding agents in persistent, reviewable microVM sandboxes.',
      social: [
        {
          icon: 'github',
          label: 'GitHub',
          href: 'https://github.com/lukaskoebe/sandbox-studio',
        },
      ],
      editLink: {
        baseUrl:
          'https://github.com/lukaskoebe/sandbox-studio/edit/main/site/src/content/docs/',
      },
      customCss: ['./src/styles/custom.css'],
      sidebar: [
        {
          label: 'Start here',
          items: ['getting-started', 'environments'],
        },
        {
          label: 'Use Studio',
          items: [
            'chat',
            'sandboxes',
            'network',
            'secrets',
            'teammates-providers',
            'templates',
            'checkpoints',
            'memory',
            'browser',
            'git-review',
            'tui',
          ],
        },
        {
          label: 'Reference',
          items: ['troubleshooting', 'developers'],
        },
      ],
    }),
  ],
})
