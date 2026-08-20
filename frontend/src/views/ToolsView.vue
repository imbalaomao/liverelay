<script setup>
defineProps({
  tools: { type: Array, default: () => [] },
  busy: { type: String, default: '' },
  releases: { type: Object, default: () => ({}) },
})
const emit = defineEmits([
  'add', 'edit', 'remove', 'probe', 'set-path', 'reset-path', 'check-update', 'upgrade',
])
</script>

<template>
  <section class="flex-1 overflow-y-auto p-6">
    <div class="flex items-center justify-between mb-5">
      <div>
        <h1 class="text-lg font-semibold">内核管理</h1>
        <p class="text-xs text-mute mt-0.5">内置内核不可删除，但可以指定本地的可执行文件</p>
      </div>
      <button class="px-4 py-2 rounded-xl bg-ink-700 text-sm hover:bg-ink-600 fade" @click="emit('add')">
        ＋ 添加自定义内核
      </button>
    </div>

    <div v-if="tools.length === 0"
         class="border border-dashed border-line rounded-2xl p-12 text-center text-mute text-sm max-w-3xl">
      内核列表为空。正常情况下这里至少有三个内置内核，若一直为空请查看日志。
    </div>

    <div class="space-y-3 max-w-3xl">
      <div v-for="k in tools" :key="k.id" class="card bg-ink-800 border border-line rounded-2xl p-4">
        <div class="flex items-start gap-4">
          <div class="w-9 h-9 rounded-xl bg-ink-700 flex items-center justify-center text-mute shrink-0">◈</div>

          <div class="min-w-0 flex-1">
            <div class="flex items-center gap-2 flex-wrap">
              <span class="font-medium text-sm">{{ k.name }}</span>
              <span v-if="k.version"
                    class="text-[10px] px-1.5 py-0.5 rounded bg-acc/10 text-acc border border-acc/20 font-mono">
                {{ k.version }}
              </span>
              <span v-else class="text-[10px] px-1.5 py-0.5 rounded bg-ink-700 text-mute">版本未探测</span>
              <span class="text-[10px] px-1.5 py-0.5 rounded bg-ink-700 text-mute">
                {{ k.builtin ? '内置' : '自定义' }}
              </span>
              <span class="text-[10px] px-1.5 py-0.5 rounded bg-ink-700 text-mute">{{ k.roleText }}</span>
              <span v-if="k.hasOverride"
                    class="text-[10px] px-1.5 py-0.5 rounded bg-warn/10 text-warn border border-warn/20">
                本地指定
              </span>
              <span v-if="k.inUse"
                    class="text-[10px] px-1.5 py-0.5 rounded bg-acc/10 text-acc border border-acc/20">使用中</span>
            </div>

            <div class="text-[11px] text-mute mt-1 break-all selectable">{{ k.path }}</div>
            <div v-if="k.capSummary" class="text-[11px] text-mute/80 mt-0.5">功能探测：{{ k.capSummary }}</div>
            <div v-if="(k.usedBy || []).length" class="text-[11px] text-mute/80 mt-0.5">
              被任务引用：{{ (k.usedBy || []).join('、') }}
            </div>

            <div v-if="releases[k.id]" class="text-[11px] mt-1"
                 :class="releases[k.id].available ? 'text-acc' : 'text-mute'">
              最新 {{ releases[k.id].latest }}（{{ releases[k.id].sizeMb }} MB）· {{ releases[k.id].note }}
            </div>
          </div>

          <div class="flex flex-wrap gap-1.5 shrink-0 justify-end max-w-[240px]">
            <button class="px-2.5 py-1.5 rounded-lg text-xs bg-ink-700 hover:bg-ink-600 fade"
                    :disabled="busy === k.id" :class="busy === k.id ? 'opacity-50' : ''"
                    @click="emit('probe', k.id)">
              {{ busy === k.id ? '…' : '探测' }}
            </button>
            <button class="px-2.5 py-1.5 rounded-lg text-xs bg-ink-700 hover:bg-ink-600 fade"
                    @click="emit('set-path', k.id)">指定本地文件</button>
            <button v-if="k.hasOverride" class="px-2.5 py-1.5 rounded-lg text-xs bg-ink-700 hover:bg-ink-600 fade"
                    @click="emit('reset-path', k.id)">恢复默认</button>
            <button v-if="k.canUpdate" class="px-2.5 py-1.5 rounded-lg text-xs bg-ink-700 hover:bg-ink-600 fade"
                    :disabled="busy === k.id" :class="busy === k.id ? 'opacity-50' : ''"
                    @click="emit('check-update', k.id)">检查更新</button>
            <button v-if="k.canUpdate && releases[k.id]"
                    class="px-2.5 py-1.5 rounded-lg text-xs bg-acc/15 text-acc border border-acc/25 hover:bg-acc/25 fade"
                    :disabled="busy === k.id || k.inUse"
                    :class="busy === k.id || k.inUse ? 'opacity-40 cursor-not-allowed' : ''"
                    @click="!k.inUse && emit('upgrade', k.id)">下载更新</button>
            <button v-if="!k.builtin" class="px-2.5 py-1.5 rounded-lg text-xs bg-ink-700 hover:bg-ink-600 fade"
                    @click="emit('edit', k.id)">✎</button>
            <button v-if="!k.builtin"
                    class="px-2.5 py-1.5 rounded-lg text-xs bg-bad/15 text-bad border border-bad/25 hover:bg-bad/25 fade"
                    @click="emit('remove', k.id)">删除</button>
          </div>
        </div>
      </div>
    </div>
  </section>
</template>
