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
        'AI teammates that work safely on your computer: Claude Code, Codex and OpenCode in their own microVM sandboxes.',
      logo: { src: './src/assets/logo.svg' },
      head: [
        {
          tag: 'meta',
          attrs: {
            property: 'og:image',
            content: 'https://lukaskoebe.github.io/sandbox-studio/video/developers.jpg',
          },
        },
        { tag: 'meta', attrs: { name: 'twitter:card', content: 'summary_large_image' } },
        { tag: 'meta', attrs: { name: 'theme-color', content: '#0b0e10' } },
      ],
      components: {
        // The landing page's hero, with the install command and the overview video.
        Hero: './src/components/Hero.astro',
      },
      expressiveCode: {
        styleOverrides: {
          borderRadius: '0.75rem',
          borderColor: 'var(--sl-color-hairline-light)',
          codeFontFamily: 'var(--__sl-font-mono)',
          uiFontFamily: 'var(--__sl-font)',
          codeFontSize: '0.875rem',
          frames: {
            shadowColor: 'transparent',
            editorTabBarBackground: 'var(--ss-surface)',
            terminalTitlebarBackground: 'var(--ss-surface)',
            terminalBackground: 'var(--ss-code-bg)',
            editorBackground: 'var(--ss-code-bg)',
          },
        },
      },
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
            'cli',
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
