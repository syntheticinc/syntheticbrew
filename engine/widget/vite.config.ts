import { defineConfig } from 'vite';
import { resolve } from 'path';

export default defineConfig({
  build: {
    lib: {
      entry: resolve(__dirname, 'src/index.ts'),
      name: 'SyntheticBrewWidget',
      formats: ['iife'],
      fileName: () => 'widget.js',
    },
    minify: 'terser',
    outDir: 'dist',
    emptyOutDir: true,
    terserOptions: {
      compress: {
        drop_console: false,
        passes: 2,
      },
    },
  },
});
