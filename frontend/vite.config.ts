import { defineConfig } from 'vite'
import vue from '@vitejs/plugin-vue'
import tailwindcss from '@tailwindcss/vite'

export default defineConfig({
  base: '/app/port-forwarding-fnos/',
  plugins: [vue(), tailwindcss()],
})
