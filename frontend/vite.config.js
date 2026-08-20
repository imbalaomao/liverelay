import { defineConfig } from 'vite'
import vue from '@vitejs/plugin-vue'
import tailwindcss from '@tailwindcss/vite'

// dev 模式的 HMR 需要连 localhost 的 vite 服务；生产构建必须剔除，
// 否则打包产物的 CSP 白名单里平白多出 http://localhost:*（XSS 红线）。
const cspDevConnect = {
  name: 'csp-dev-connect',
  transformIndexHtml(html, ctx) {
    // replaceAll 而非 replace：占位符在注释里也出现一次，只替换首处会漏掉真正的 CSP
    return html.replaceAll(
      '%CSP_DEV_CONNECT%',
      ctx.server ? " ws://localhost:* http://localhost:*" : ''
    )
  },
}

export default defineConfig({
  plugins: [vue(), tailwindcss(), cspDevConnect],
  build: {
    // 资源全部打进 dist，由 Go 侧 embed；不引用任何远程资源（CSP 红线）
    assetsInlineLimit: 4096,
    chunkSizeWarningLimit: 1024,
  },
})
