import { defineConfig } from 'vite'
import react from '@vitejs/plugin-react'

export default defineConfig({
  plugins: [react()],
  base: '/observe/',
  build: {
    outDir: '../observe',
    emptyOutDir: true,
  },
})
