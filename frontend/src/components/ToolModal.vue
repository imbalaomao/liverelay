<script setup>
import { computed, ref, watch } from 'vue'

const props = defineProps({
  open: { type: Boolean, default: false },
  tool: { type: Object, default: null },
  // 文件选择要由父组件发起（它才有 api），emit 拿不到返回值
  picker: { type: Function, default: null },
})
const emit = defineEmits(['close', 'submit'])

function blank() {
  return { id: '', name: '', path: '', role: 'fetch', argTemplate: '', probeTemplate: '' }
}

const form = ref(blank())

watch(
  () => [props.open, props.tool],
  () => {
    if (!props.open) return
    form.value = props.tool
      ? {
          id: props.tool.id,
          name: props.tool.name,
          path: props.tool.path,
          role: props.tool.role,
          argTemplate: (props.tool.argTemplate || []).join(' '),
          probeTemplate: (props.tool.probeTemplate || []).join(' '),
        }
      : blank()
  },
  { immediate: true },
)

const editing = computed(() => !!props.tool)

// ID 会出现在配置文件与日志里，放开中文和空格只会在排查问题时添乱
const idOk = computed(() => /^[A-Za-z0-9][A-Za-z0-9_-]{0,63}$/.test(form.value.id))
const canSubmit = computed(() => idOk.value && form.value.name.trim() && form.value.path.trim())

async function pick() {
  if (!props.picker) return
  const p = await props.picker()
  if (p) form.value.path = p
}

function submit() {
  if (!canSubmit.value) return
  emit('submit', {
    id: form.value.id,
    name: form.value.name.trim(),
    path: form.value.path.trim(),
    role: form.value.role,
    argTemplate: form.value.argTemplate.split(/\s+/).filter(Boolean),
    probeTemplate: form.value.probeTemplate.split(/\s+/).filter(Boolean),
  })
}
</script>

<template>
  <div
    v-if="open"
    class="fixed inset-0 bg-black/60 backdrop-blur-sm flex items-center justify-center z-50"
    @click.self="emit('close')"
  >
    <div class="w-[560px] bg-ink-800 border border-line rounded-2xl p-6 shadow-2xl">
      <div class="flex items-center justify-between mb-5">
        <h2 class="font-semibold">{{ editing ? '编辑内核' : '添加自定义内核' }}</h2>
        <button class="text-mute hover:text-white fade" @click="emit('close')">✕</button>
      </div>

      <div class="space-y-4 text-sm">
        <div class="grid grid-cols-2 gap-3">
          <label class="space-y-1.5 block">
            <span class="text-xs text-mute">标识（ASCII 字母数字与 - _）</span>
            <input v-model="form.id" :disabled="editing" placeholder="streamlink-drm"
                   class="w-full bg-ink-900 border rounded-xl px-3 py-2 fade disabled:opacity-50"
                   :class="form.id && !idOk ? 'border-bad' : 'border-line'" />
          </label>
          <label class="space-y-1.5 block">
            <span class="text-xs text-mute">显示名称</span>
            <input v-model="form.name" placeholder="streamlink-drm"
                   class="w-full bg-ink-900 border border-line rounded-xl px-3 py-2 fade" />
          </label>
        </div>

        <label class="space-y-1.5 block">
          <span class="text-xs text-mute">可执行文件路径（支持完整路径）</span>
          <div class="flex gap-2">
            <input v-model="form.path" placeholder="D:\tools\streamlink-drm.exe"
                   class="flex-1 bg-ink-900 border border-line rounded-xl px-3 py-2 fade text-xs" />
            <button class="px-3 rounded-xl bg-ink-700 hover:bg-ink-600 fade text-xs" @click="pick">浏览</button>
          </div>
        </label>

        <label class="space-y-1.5 block">
          <span class="text-xs text-mute">用途</span>
          <select v-model="form.role" class="w-full bg-ink-900 border border-line rounded-xl px-3 py-2 fade">
            <option value="fetch">抓流</option>
            <option value="record">录制</option>
            <option value="both">抓流 / 录制</option>
          </select>
        </label>

        <label class="space-y-1.5 block">
          <span class="text-xs text-mute">参数模板（占位符 {url} {quality}）</span>
          <input v-model="form.argTemplate" placeholder="{url} {quality} -O"
                 class="w-full bg-ink-900 border border-line rounded-xl px-3 py-2 fade text-xs font-mono" />
        </label>

        <label class="space-y-1.5 block">
          <span class="text-xs text-mute">开播探测参数（留空则不能用于无人值守）</span>
          <input v-model="form.probeTemplate" placeholder="--json {url}"
                 class="w-full bg-ink-900 border border-line rounded-xl px-3 py-2 fade text-xs font-mono" />
        </label>

        <div class="flex justify-end gap-2 pt-1">
          <button class="px-4 py-2 rounded-xl bg-ink-700 hover:bg-ink-600 fade text-sm" @click="emit('close')">
            取消
          </button>
          <button
            class="px-4 py-2 rounded-xl font-semibold fade text-sm"
            :class="canSubmit ? 'bg-acc text-ink-950 hover:bg-acc-dim' : 'bg-ink-700 text-mute cursor-not-allowed'"
            :disabled="!canSubmit"
            @click="submit"
          >
            {{ editing ? '保存' : '添加' }}
          </button>
        </div>
      </div>
    </div>
  </div>
</template>
