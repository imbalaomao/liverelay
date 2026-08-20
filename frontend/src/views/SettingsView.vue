<script setup>
import { computed, ref } from 'vue'
import Toggle from '../components/Toggle.vue'

const props = defineProps({
  settings: { type: Object, required: true },
  weibo: { type: Object, required: true },
  env: { type: Object, required: true },
  weiboBusy: { type: Boolean, default: false },
})
const emit = defineEmits([
  'save', 'pick-dir', 'save-cookie', 'clear-cookie', 'check-cookie',
])

const cookie = ref('')

// 端口为 0 时显示空白而不是"0"——那是"还没填"，不是一个真实的端口号
const proxyPort = computed({
  get: () => (props.settings.proxy.port ? String(props.settings.proxy.port) : ''),
  set: (v) => {
    props.settings.proxy.port = v === '' ? 0 : Number(v)
  },
})

// 状态色：无法判断（多半是断网）用黄色而不是红色——
// 那不是用户的错，也不需要他重新登录
const STATUS_CLASS = {
  valid: 'bg-acc/10 text-acc border-acc/20',
  expired: 'bg-bad/10 text-bad border-bad/25',
  unknown: 'bg-warn/10 text-warn border-warn/20',
  absent: 'bg-ink-700 text-mute border-line',
}

function submitCookie() {
  const v = cookie.value.trim()
  if (!v) return
  emit('save-cookie', v)
  cookie.value = ''
}
</script>

<template>
  <section class="flex-1 overflow-y-auto p-6">
    <h1 class="text-lg font-semibold mb-5">设置</h1>

    <div class="space-y-4 max-w-2xl">
      <!-- 代理 -->
      <div class="bg-ink-800 border border-line rounded-2xl p-5">
        <h2 class="text-sm font-semibold mb-4">网络代理</h2>
        <div class="space-y-3">
          <Toggle v-model="settings.proxy.enabled" title="启用代理"
                  desc="同时作用于抓流内核、更新器与微博接口" />
          <div class="grid grid-cols-4 gap-3 text-sm" :class="settings.proxy.enabled ? '' : 'opacity-50'">
            <select v-model="settings.proxy.type" :disabled="!settings.proxy.enabled"
                    class="bg-ink-900 border border-line rounded-xl px-3 py-2 fade">
              <option value="http">HTTP</option>
              <option value="socks5">SOCKS5</option>
            </select>
            <input v-model="settings.proxy.host" :disabled="!settings.proxy.enabled" placeholder="127.0.0.1"
                   class="col-span-2 bg-ink-900 border border-line rounded-xl px-3 py-2 fade" />
            <input v-model="proxyPort" :disabled="!settings.proxy.enabled" type="number"
                   placeholder="7890" class="bg-ink-900 border border-line rounded-xl px-3 py-2 fade" />
          </div>
          <div class="grid grid-cols-2 gap-3 text-sm" :class="settings.proxy.enabled ? '' : 'opacity-50'">
            <input v-model="settings.proxy.username" :disabled="!settings.proxy.enabled" placeholder="用户名（可选）"
                   class="bg-ink-900 border border-line rounded-xl px-3 py-2 fade" />
            <input v-model="settings.proxy.password" :disabled="!settings.proxy.enabled" type="password"
                   placeholder="密码（可选）" class="bg-ink-900 border border-line rounded-xl px-3 py-2 fade" />
          </div>
        </div>
      </div>

      <!-- 开关 -->
      <div class="bg-ink-800 border border-line rounded-2xl p-5 space-y-4">
        <Toggle v-model="settings.closeToTray" title="关闭到托盘"
                desc="关闭窗口仅隐藏界面，推流继续运行" />
        <Toggle v-model="settings.preventSleep" title="推流时保持电脑不休眠"
                desc="有任务在推流时阻止系统睡眠；不会一直点亮屏幕" />
      </div>

      <!-- 运行参数 -->
      <div class="bg-ink-800 border border-line rounded-2xl p-5">
        <h2 class="text-sm font-semibold mb-4">运行参数</h2>
        <div class="grid grid-cols-2 gap-4 text-sm">
          <label class="space-y-1.5 block">
            <span class="text-xs text-mute">同时推流上限（1–16）</span>
            <input v-model.number="settings.maxConcurrent" type="number" min="1" max="16"
                   class="w-full bg-ink-900 border border-line rounded-xl px-3 py-2 fade" />
          </label>
          <label class="space-y-1.5 block">
            <span class="text-xs text-mute">开播探测间隔（30–300 秒）</span>
            <input v-model.number="settings.probeIntervalSec" type="number" min="30" max="300"
                   class="w-full bg-ink-900 border border-line rounded-xl px-3 py-2 fade" />
          </label>
        </div>
        <p class="text-[11px] text-mute mt-3">
          目标机配置有限时把上限调小些；探测间隔越短，后台起的进程越频繁。
        </p>
      </div>

      <!-- 微博直播 -->
      <div class="bg-ink-800 border border-line rounded-2xl p-5">
        <div class="flex items-center justify-between mb-3">
          <h2 class="text-sm font-semibold">微博直播</h2>
          <span class="text-[11px] px-2 py-1 rounded-md border"
                :class="STATUS_CLASS[weibo.status] || STATUS_CLASS.absent">
            {{ weibo.statusText }}
          </span>
        </div>

        <p v-if="weibo.detail" class="text-[11px] text-mute mb-3 break-words">{{ weibo.detail }}</p>
        <p v-if="weibo.checkedAt" class="text-[11px] text-mute mb-3">上次检测：{{ weibo.checkedAt }}（每 3 天自动复检）</p>

        <label class="space-y-1.5 block text-sm">
          <span class="text-xs text-mute">
            微博 Cookie（登录网页版后从浏览器开发者工具里复制整条 Cookie 请求头）
          </span>
          <textarea v-model="cookie" rows="3" placeholder="SUB=...; SUBP=...; XSRF-TOKEN=..."
                    class="w-full bg-ink-900 border border-line rounded-xl px-3 py-2 fade text-xs font-mono"></textarea>
        </label>
        <p class="text-[11px] text-mute mt-2">
          Cookie 会用 Windows 账户密钥加密后存在本地，不写进配置文件，也不会出现在日志里。
        </p>

        <div class="flex gap-2 mt-3">
          <button class="px-3 py-2 rounded-xl bg-acc text-ink-950 text-xs font-semibold hover:bg-acc-dim fade"
                  :disabled="weiboBusy || !cookie.trim()"
                  :class="weiboBusy || !cookie.trim() ? 'opacity-40 cursor-not-allowed' : ''"
                  @click="submitCookie">
            {{ weiboBusy ? '验证中…' : '验证并保存' }}
          </button>
          <button class="px-3 py-2 rounded-xl bg-ink-700 hover:bg-ink-600 fade text-xs"
                  :disabled="weiboBusy" @click="emit('check-cookie')">立即检测</button>
          <button class="px-3 py-2 rounded-xl bg-ink-700 hover:bg-bad/25 hover:text-bad fade text-xs"
                  @click="emit('clear-cookie')">清除</button>
        </div>
      </div>

      <!-- 存储 -->
      <div class="bg-ink-800 border border-line rounded-2xl p-5">
        <h2 class="text-sm font-semibold mb-3">存储</h2>
        <div class="flex items-center gap-3 text-sm">
          <span class="text-mute shrink-0 text-xs">录制目录</span>
          <input v-model="settings.recordDir" :placeholder="env.dataDir + '\\recordings'"
                 class="flex-1 bg-ink-900 border border-line rounded-xl px-3 py-2 fade text-xs" />
          <button class="px-3 py-2 rounded-xl bg-ink-700 hover:bg-ink-600 fade text-xs"
                  @click="emit('pick-dir')">浏览</button>
        </div>
        <div class="flex items-center gap-3 text-sm mt-3">
          <span class="text-mute shrink-0 text-xs">运行模式</span>
          <span class="text-xs px-2 py-1 rounded-md bg-acc/10 text-acc border border-acc/20">
            {{ env.mode }}模式
          </span>
          <code class="text-[11px] text-mute truncate selectable">{{ env.dataDir }}</code>
        </div>
      </div>

      <div class="flex justify-end pb-2">
        <button class="px-5 py-2 rounded-xl bg-acc text-ink-950 text-sm font-semibold hover:bg-acc-dim fade"
                @click="emit('save')">
          保存设置
        </button>
      </div>
    </div>
  </section>
</template>
