<script setup>
import { computed, ref, watch } from 'vue'

const props = defineProps({
  open: { type: Boolean, default: false },
  // 编辑时传入后端给的表单副本（密钥已抹掉，带 hasKey 标记）
  task: { type: Object, default: null },
  tools: { type: Array, default: () => [] },
  weiboUsable: { type: Boolean, default: false },
})
const emit = defineEmits(['close', 'submit'])

const PROTOCOLS = ['rtmp', 'rtmps', 'srt', 'udp', 'hls']
const QUALITIES = ['best', '1080p', '720p', '480p', 'worst']

function blank() {
  return {
    id: '',
    name: '',
    sourceUrl: '',
    toolId: 'streamlink',
    quality: 'best',
    targets: [{ proto: 'rtmp', url: '', key: '', hasKey: false }],
    unattended: false,
    autoRecord: false,
    recordToolId: '',
    customArgs: '',
    weiboLive: false,
  }
}

const form = ref(blank())

watch(
  () => [props.open, props.task],
  () => {
    if (!props.open) return
    form.value = props.task ? normalize(props.task) : blank()
  },
  { immediate: true },
)

// 后端返回的字段可能缺省（老配置、微博任务没有目标），补齐再进表单
function normalize(t) {
  const f = { ...blank(), ...t }
  f.targets = (t.targets || []).map((tg) => ({
    proto: tg.proto || 'rtmp',
    url: tg.url || '',
    key: '',
    hasKey: !!tg.hasKey,
  }))
  if (f.targets.length === 0) f.targets = [{ proto: 'rtmp', url: '', key: '', hasKey: false }]
  return f
}

const fetchTools = computed(() => props.tools.filter((t) => t.role === 'fetch' || t.role === 'both'))
const recordTools = computed(() => props.tools.filter((t) => t.role === 'record' || t.role === 'both'))

const selectedTool = computed(() => props.tools.find((t) => t.id === form.value.toolId))
// 自定义内核才给自定义参数：内置内核的参数模板由程序管，用户乱加只会推流失败
const isCustomTool = computed(() => selectedTool.value && !selectedTool.value.builtin)

const title = computed(() => (form.value.id ? '编辑任务' : '新建任务'))

function addTarget() {
  form.value.targets.push({ proto: 'rtmp', url: '', key: '', hasKey: false })
}
function removeTarget(i) {
  form.value.targets.splice(i, 1)
}

// 命令预览让用户在保存前就看清程序到底会执行什么。
// 密钥一律以 *** 代替：这行文本会显示在屏幕上，也可能被截图。
const preview = computed(() => {
  const tool = selectedTool.value
  const name = tool ? tool.name : form.value.toolId
  const extra = isCustomTool.value && form.value.customArgs ? ' ' + form.value.customArgs : ''
  const outs = form.value.targets
    .filter((t) => t.url)
    .map((t) => `${t.url.replace(/\/+$/, '')}/***`)
  if (form.value.weiboLive) outs.push('rtmp://push.alive.sinaimg.cn/alive/***')
  const sink = outs.length ? outs.join('  ') : '（尚未填写推流目标）'
  return `${name} "${form.value.sourceUrl || '<直播源地址>'}" ${form.value.quality} -O${extra}  |  ffmpeg -i pipe:0 -c copy -f flv ${sink}`
})

const canSubmit = computed(() => {
  if (!form.value.name.trim() || !form.value.sourceUrl.trim()) return false
  const hasTarget = form.value.targets.some((t) => t.url.trim())
  return hasTarget || form.value.weiboLive
})

function submit() {
  if (!canSubmit.value) return
  const out = JSON.parse(JSON.stringify(form.value))
  // 空地址的行是用户加了没填的，直接丢掉，别送到后端换一句校验失败
  out.targets = out.targets.filter((t) => t.url.trim())
  emit('submit', out)
}
</script>

<template>
  <div
    v-if="open"
    class="fixed inset-0 bg-black/60 backdrop-blur-sm flex items-center justify-center z-50"
    @click.self="emit('close')"
  >
    <div class="w-[600px] max-h-[88vh] overflow-y-auto bg-ink-800 border border-line rounded-2xl p-6 shadow-2xl">
      <div class="flex items-center justify-between mb-5">
        <h2 class="font-semibold">{{ title }}</h2>
        <button class="text-mute hover:text-white fade" @click="emit('close')">✕</button>
      </div>

      <div class="space-y-4 text-sm">
        <div class="grid grid-cols-2 gap-3">
          <label class="space-y-1.5 block">
            <span class="text-xs text-mute">任务名称</span>
            <input v-model="form.name" placeholder="例如 XX 直播间"
                   class="w-full bg-ink-900 border border-line rounded-xl px-3 py-2 fade" />
          </label>
          <label class="space-y-1.5 block">
            <span class="text-xs text-mute">抓流内核</span>
            <select v-model="form.toolId" class="w-full bg-ink-900 border border-line rounded-xl px-3 py-2 fade">
              <option v-for="t in fetchTools" :key="t.id" :value="t.id">
                {{ t.name }}{{ t.builtin ? '（内置）' : '（自定义）' }}
              </option>
            </select>
          </label>
        </div>

        <label class="space-y-1.5 block">
          <span class="text-xs text-mute">直播源地址</span>
          <input v-model="form.sourceUrl" placeholder="https://live.example.com/room/123"
                 class="w-full bg-ink-900 border border-line rounded-xl px-3 py-2 fade" />
        </label>

        <div class="grid grid-cols-2 gap-3">
          <label class="space-y-1.5 block">
            <span class="text-xs text-mute">画质</span>
            <select v-model="form.quality" class="w-full bg-ink-900 border border-line rounded-xl px-3 py-2 fade">
              <option v-for="q in QUALITIES" :key="q" :value="q">{{ q }}</option>
            </select>
          </label>
          <label class="space-y-1.5 block">
            <span class="text-xs text-mute">录制内核（自动录制时使用）</span>
            <select v-model="form.recordToolId" class="w-full bg-ink-900 border border-line rounded-xl px-3 py-2 fade">
              <option value="">与抓流一致</option>
              <option v-for="t in recordTools" :key="t.id" :value="t.id">{{ t.name }}</option>
            </select>
          </label>
        </div>

        <div class="space-y-2">
          <span class="text-xs text-mute">推流目标（可多条）</span>
          <div v-for="(tg, i) in form.targets" :key="i" class="flex gap-2">
            <select v-model="tg.proto" class="bg-ink-900 border border-line rounded-xl px-2 py-2 fade w-24 text-xs">
              <option v-for="p in PROTOCOLS" :key="p" :value="p">{{ p.toUpperCase() }}</option>
            </select>
            <input v-model="tg.url" placeholder="rtmp://live-push.example.com/live/"
                   class="flex-1 bg-ink-900 border border-line rounded-xl px-3 py-2 fade text-xs" />
            <input v-model="tg.key" type="password"
                   :placeholder="tg.hasKey ? '已设置（留空则不变）' : '推流密钥'"
                   class="w-40 bg-ink-900 border border-line rounded-xl px-3 py-2 fade text-xs" />
            <button v-if="form.targets.length > 1"
                    class="px-2 rounded-lg text-xs text-mute hover:text-bad fade"
                    @click="removeTarget(i)">✕</button>
          </div>
          <button class="text-xs text-acc hover:underline" @click="addTarget">＋ 添加目标</button>
        </div>

        <div v-if="isCustomTool" class="space-y-1.5">
          <span class="text-xs text-mute">自定义参数（逐字传给内核，支持引号）</span>
          <textarea v-model="form.customArgs" rows="2"
                    placeholder='--decryption-key "xxxx" --http-header "Referer=https://..."'
                    class="w-full bg-ink-900 border border-line rounded-xl px-3 py-2 fade text-xs font-mono"></textarea>
        </div>

        <div class="space-y-3 pt-1">
          <label class="flex items-center gap-2 cursor-pointer text-xs text-mute">
            <input type="checkbox" v-model="form.unattended" class="accent-emerald-400" />
            无人值守（探测到开播自动推流）
          </label>
          <label class="flex items-center gap-2 cursor-pointer text-xs text-mute">
            <input type="checkbox" v-model="form.autoRecord" class="accent-emerald-400" />
            自动录制
          </label>
          <label class="flex items-center gap-2 text-xs"
                 :class="weiboUsable ? 'cursor-pointer text-mute' : 'opacity-50'">
            <input type="checkbox" v-model="form.weiboLive" :disabled="!weiboUsable" class="accent-emerald-400" />
            微博直播（开播时自动取推流地址并给出观看链接）
          </label>
          <p v-if="!weiboUsable" class="text-[11px] text-warn pl-6">
            需先在「设置」里录入有效的微博 Cookie
          </p>
        </div>

        <div class="bg-ink-950 border border-line rounded-xl p-3">
          <div class="text-[10px] text-mute mb-1">命令预览（密钥已脱敏）</div>
          <code class="text-[11px] text-acc/90 break-all font-mono selectable">{{ preview }}</code>
        </div>

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
            {{ form.id ? '保存' : '创建任务' }}
          </button>
        </div>
      </div>
    </div>
  </div>
</template>
