<script setup>
import { computed, ref } from 'vue'

const props = defineProps({
  tasks: { type: Array, default: () => [] },
  events: { type: Object, default: () => ({}) },
})
const emit = defineEmits(['create', 'edit', 'remove', 'start', 'stop', 'open-log', 'copy'])

const expanded = ref('')

function toggleLog(id) {
  expanded.value = expanded.value === id ? '' : id
  if (expanded.value) emit('open-log', id)
}

const runningCount = computed(
  () => props.tasks.filter((t) => ['running', 'starting', 'reconnecting', 'queued'].includes(t.state)).length,
)

// 状态决定圆点颜色与是否呼吸。只有真正在动的状态才动，省一点核显
const STATE_STYLE = {
  running: { dot: 'bg-acc', pulse: true, border: 'border-acc/25' },
  starting: { dot: 'bg-acc', pulse: true, border: 'border-acc/25' },
  reconnecting: { dot: 'bg-warn', pulse: true, border: 'border-warn/25' },
  queued: { dot: 'bg-warn', pulse: false, border: 'border-line' },
  monitoring: { dot: 'bg-warn', pulse: true, border: 'border-warn/20' },
  failed: { dot: 'bg-bad', pulse: false, border: 'border-bad/30' },
  idle: { dot: 'bg-ink-600', pulse: false, border: 'border-line' },
}
const styleOf = (s) => STATE_STYLE[s] || STATE_STYLE.idle

const canStop = (s) => ['running', 'starting', 'reconnecting', 'queued'].includes(s)
</script>

<template>
  <section class="flex-1 overflow-y-auto p-6">
    <div class="flex items-center justify-between mb-5">
      <div>
        <h1 class="text-lg font-semibold">推流任务</h1>
        <p class="text-xs text-mute mt-0.5">{{ tasks.length }} 个任务 · {{ runningCount }} 路推流中</p>
      </div>
      <button class="px-4 py-2 rounded-xl bg-acc text-ink-950 text-sm font-semibold hover:bg-acc-dim fade"
              @click="emit('create')">
        ＋ 新建任务
      </button>
    </div>

    <div v-if="tasks.length === 0"
         class="border border-dashed border-line rounded-2xl p-12 text-center text-mute text-sm max-w-3xl">
      还没有任务。点右上角新建一个，填好直播源与推流地址就能开播。
    </div>

    <div class="space-y-3 max-w-3xl">
      <div v-for="t in tasks" :key="t.id"
           class="card bg-ink-800 border rounded-2xl p-4"
           :class="styleOf(t.state).border">
        <div class="flex items-center gap-3 flex-wrap">
          <span class="dot" :class="[styleOf(t.state).dot, styleOf(t.state).pulse ? 'pulse' : '']" />
          <span class="font-medium">{{ t.name }}</span>
          <span class="text-xs px-2 py-0.5 rounded-md bg-ink-700 text-slate-300">{{ t.stateText }}</span>
          <span v-if="t.unattended"
                class="text-xs px-2 py-0.5 rounded-md bg-warn/10 text-warn border border-warn/20">无人值守</span>
          <span v-if="t.autoRecord"
                class="text-xs px-2 py-0.5 rounded-md bg-ink-700 text-slate-300">自动录制</span>
          <span class="text-xs text-mute">→</span>
          <span v-for="(tg, i) in (t.targets || [])" :key="i"
                class="text-xs px-2 py-0.5 rounded-md bg-ink-700 text-slate-300">{{ tg }}</span>

          <div class="ml-auto flex gap-1.5">
            <button v-if="canStop(t.state)"
                    class="px-2.5 py-1.5 rounded-lg text-xs bg-ink-700 hover:bg-ink-600 fade"
                    @click="emit('stop', t.id)">■ 停止</button>
            <button v-else
                    class="px-2.5 py-1.5 rounded-lg text-xs bg-acc/15 text-acc border border-acc/25 hover:bg-acc/25 fade"
                    @click="emit('start', t.id)">▶ 开播</button>
            <button class="px-2.5 py-1.5 rounded-lg text-xs bg-ink-700 hover:bg-ink-600 fade"
                    :disabled="canStop(t.state)"
                    :class="canStop(t.state) ? 'opacity-40 cursor-not-allowed' : ''"
                    @click="!canStop(t.state) && emit('edit', t.id)">✎</button>
            <button class="px-2.5 py-1.5 rounded-lg text-xs bg-ink-700 hover:bg-bad/25 hover:text-bad fade"
                    :disabled="canStop(t.state)"
                    :class="canStop(t.state) ? 'opacity-40 cursor-not-allowed' : ''"
                    @click="!canStop(t.state) && emit('remove', t.id)">🗑</button>
          </div>
        </div>

        <div class="mt-3 flex gap-4 text-[11px] text-mute flex-wrap">
          <span>内核 <b class="text-slate-300 font-normal">{{ t.toolName }}</b></span>
          <span v-if="t.quality">画质 <b class="text-slate-300 font-normal">{{ t.quality }}</b></span>
          <span class="truncate max-w-[280px]">源 <b class="text-slate-300 font-normal">{{ t.sourceUrl }}</b></span>
        </div>

        <div v-if="t.weiboLive && t.watchUrl" class="mt-3 flex items-center gap-2 text-[11px]">
          <span class="text-mute shrink-0">微博观看链接</span>
          <code class="flex-1 truncate text-acc/90 font-mono selectable">{{ t.watchUrl }}</code>
          <button class="px-2 py-1 rounded-lg bg-ink-700 hover:bg-ink-600 fade shrink-0"
                  @click="emit('copy', t.watchUrl)">复制</button>
        </div>

        <div v-if="t.lastMsg" class="mt-3 flex items-start gap-2 text-[11px]">
          <span class="text-mute shrink-0">最近</span>
          <span class="flex-1 text-slate-400 break-words">{{ t.lastMsg }}</span>
          <button class="text-mute hover:text-slate-200 fade shrink-0" @click="toggleLog(t.id)">
            {{ expanded === t.id ? '收起日志' : '查看日志' }}
          </button>
        </div>

        <div v-if="expanded === t.id"
             class="mt-3 bg-ink-950 border border-line rounded-xl p-3 max-h-56 overflow-y-auto space-y-1">
          <div v-if="!(events[t.id] || []).length" class="text-[11px] text-mute">暂无记录</div>
          <div v-for="(ev, i) in events[t.id] || []" :key="i" class="text-[11px] font-mono flex gap-2">
            <span class="text-mute shrink-0">{{ ev.at }}</span>
            <span class="text-slate-400 break-all selectable">{{ ev.msg }}</span>
          </div>
        </div>
      </div>
    </div>
  </section>
</template>
