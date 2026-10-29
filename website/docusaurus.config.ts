import {themes as prismThemes} from 'prism-react-renderer';
import type {Config} from '@docusaurus/types';
import type * as Preset from '@docusaurus/preset-classic';

// This runs in Node.js - Don't use client-side code here (browser APIs, JSX...)

const config: Config = {
  title: 'git-zf',
  tagline:
    'A TUI powered CLI that wraps a git-flow workflow (issue → branch → commit → review → close) with optional issue-tracker integration.',
  favicon: 'img/git-zf-logo.webp',

  future: {
    v4: true,
  },

  // The site is served from the root of its domain. Replace the host.
  url: 'https://example.org',
  baseUrl: '/',

  onBrokenLinks: 'throw',
  onBrokenAnchors: 'throw',

  markdown: {
    // .md is CommonMark: help text and prose hold <IssueID> and {issue-id},
    // which MDX would read as JSX. .mdx stays MDX.
    format: 'detect',
    hooks: {
      onBrokenMarkdownLinks: 'throw',
    },
  },

  i18n: {
    defaultLocale: 'en',
    locales: ['en'],
  },

  presets: [
    [
      'classic',
      {
        docs: {
          routeBasePath: '/',
          sidebarPath: './sidebars.ts',
        },
        blog: false,
        theme: {
          customCss: './src/css/custom.css',
        },
      } satisfies Preset.Options,
    ],
  ],

  themeConfig: {
    image: 'img/git-zf-logo.webp',
    colorMode: {
      respectPrefersColorScheme: true,
    },
    navbar: {
      title: 'git-zf',
      logo: {
        alt: 'git-zf',
        src: 'img/git-zf-logo.webp',
      },
      items: [
        {
          href: 'https://github.com/piprim/git-zf',
          label: 'GitHub',
          position: 'right',
        },
      ],
    },
    footer: {
      style: 'dark',
      copyright: 'git-zf is released under the MIT licence.',
    },
    prism: {
      theme: prismThemes.github,
      darkTheme: prismThemes.dracula,
      additionalLanguages: ['bash', 'toml'],
    },
  } satisfies Preset.ThemeConfig,
};

export default config;
