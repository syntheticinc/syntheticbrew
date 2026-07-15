import { defineConfig } from 'astro/config';
import sitemap from '@astrojs/sitemap';
import react from '@astrojs/react';

export default defineConfig({
  site: 'https://syntheticbrew.ai',
  output: 'static',
  trailingSlash: 'always',
  server: { port: 4326 },
  redirects: {
    '/discord': {
      status: 302,
      destination: 'https://discord.gg/9yDduSsVWg',
    },
  },
  integrations: [
    react(),
    sitemap({
      filter: (page) => !['/404', '/dashboard', '/login', '/register'].some((route) => page.includes(route)),
      customPages: [
        'https://syntheticbrew.ai/docs/',
      ],
    }),
  ],
});
