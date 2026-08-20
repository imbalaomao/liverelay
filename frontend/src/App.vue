<script setup>
import { ref, onMounted } from 'vue'
import { Env } from '../wailsjs/go/main/App'
import { WindowMinimise, WindowToggleMaximise, Quit } from '../wailsjs/runtime/runtime'

const view = ref('tasks')
const env = ref({ version: '', mode: '', dataDir: '' })

const NAV = [
  { key: 'tasks', icon: '▣', label: '任务' },
  { key: 'kernels', icon: '◈', label: '内核' },
  { key: 'settings', icon: '⚙', label: '设置' },
]

onMounted(async () => {
  env.value = await Env()
})
</script>

<template>
  <div class="flex h-full">
    <!-- 侧边栏 -->
    <aside class="w-52 shrink-0 glass border-r border-line flex flex-col">
      <div class="px-5 pt-5 pb-4 flex items-center gap-2.5">
        <div
          class="w-8 h-8 rounded-xl bg-acc/15 border border-acc/30 flex items-center justify-center text-acc font-bold"
        >
          ◤
        </div>
        <div>
          <div class="font-semibold tracking-wide">LiveRelay</div>
          <div class="text-[10px] text-mute">自动直播转发</div>
        </div>
      </div>

      <nav class="px-3 space-y-1 mt-2">
        <button
          v-for="n in NAV"
          :key="n.key"
          class="w-full text-left px-3.5 py-2.5 rounded-xl fade border"
          :class="
            view === n.key
              ? 'bg-acc/10 text-acc border-acc/20'
              : 'text-slate-300 hover:bg-ink-700 border-transparent'
          "
          @click="view = n.key"
        >
          {{ n.icon }}&nbsp;&nbsp;{{ n.label }}
        </button>
      </nav>

      <div class="mt-auto px-4 py-4 border-t border-line text-[11px] text-mute space-y-1">
        <div class="flex justify-between">
          <span>版本</span><span class="text-slate-300">{{ env.version || '—' }}</span>
        </div>
        <div class="flex justify-between">
          <span>模式</span><span class="text-acc">{{ env.mode || '—' }}</span>
        </div>
      </div>
    </aside>

    <!-- 主区域 -->
    <main class="flex-1 flex flex-col min-w-0">
      <!-- 自绘标题栏 -->
      <header
        class="h-11 shrink-0 flex items-center justify-between px-4 border-b border-line glass"
        style="--wails-draggable: drag"
      >
        <div class="text-xs text-mute truncate">{{ env.dataDir }}</div>
        <div class="flex items-center gap-1" style="--wails-draggable: no-drag">
          <button class="w-8 h-7 rounded-lg fade hover:bg-ink-700 text-mute" @click="WindowMinimise()">─</button>
          <button class="w-8 h-7 rounded-lg fade hover:bg-ink-700 text-mute" @click="WindowToggleMaximise()">▢</button>
          <button class="w-8 h-7 rounded-lg fade hover:bg-bad/20 hover:text-bad text-mute" @click="Quit()">✕</button>
        </div>
      </header>

      <section class="flex-1 overflow-auto p-6">
        <div v-if="view === 'tasks'" class="text-mute text-sm">推流任务（M3-7 接入）</div>
        <div v-else-if="view === 'kernels'" class="text-mute text-sm">内核管理（M3-3 / M3-7 接入）</div>
        <div v-else class="text-mute text-sm">设置（M3-7 接入）</div>
      </section>
    </main>
  </div>
</template>
