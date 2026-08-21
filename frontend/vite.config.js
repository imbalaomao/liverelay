import { writeFileSync } from 'node:fs'
import { resolve } from 'node:path'
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
      ctx.server ? ' ws://localhost:* http://localhost:*' : ''
    )
  },
}

// Go 侧的 //go:embed all:frontend/dist 要求这个目录里至少有一个文件，
// 否则干净检出连 go build ./... 都过不去（编译期就报 no matching files）。
//
// 单靠提交一个 .gitkeep 不管用：vite 每次构建都会清空 dist，占位文件一并被删，
// 于是它永远提交不上去，而本地因为总有构建产物所以察觉不到。
// emptyOutDir 又不能关——旧的 hash 资源会一直堆着，还会被 embed 进 exe 里。
// 所以只能在构建写完之后把占位文件补回来。
const keepDistTracked = {
  name: 'keep-dist-tracked',
  apply: 'build',
  closeBundle() {
    writeFileSync(resolve(import.meta.dirname, 'dist/.gitkeep'), '')
  },
}

export default defineConfig({
  plugins: [vue(), tailwindcss(), cspDevConnect, keepDistTracked],
  build: {
    // 资源全部打进 dist，由 Go 侧 embed；不引用任何远程资源（CSP 红线）
    assetsInlineLimit: 4096,
    chunkSizeWarningLimit: 1024,
  },
})
