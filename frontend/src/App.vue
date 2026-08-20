<script setup>
import { computed, onMounted, onUnmounted, reactive, ref } from 'vue'
import { EventsOn, EventsOff } from '../wailsjs/runtime/runtime'
import { api, toMessage } from './api'
import TasksView from './views/TasksView.vue'
import ToolsView from './views/ToolsView.vue'
import SettingsView from './views/SettingsView.vue'
import TaskModal from './components/TaskModal.vue'
import ToolModal from './components/ToolModal.vue'
import Toasts from './components/Toasts.vue'

const PUSH_EVENT = 'tasks:changed'

const view = ref('tasks')
const env = ref({ version: '', mode: '', dataDir: '' })
const tasks = ref([])
const tools = ref([])
const settings = ref(null)
const weibo = ref({ status: 'absent', statusText: '未录入', detail: '', checkedAt: '', usable: false })
const events = reactive({})
const releases = reactive({})

const toolBusy = ref('')
const weiboBusy = ref(false)

const taskModal = reactive({ open: false, task: null })
const toolModal = reactive({ open: false, tool: null })

// ---------- 提示 ----------

const toasts = ref([])
let toastSeq = 0

function notify(text, kind = 'info') {
  const id = ++toastSeq
  toasts.value.push({ id, text, kind })
  // 错误留久一点：用户要有时间读完再决定怎么办
  setTimeout(() => dismiss(id), kind === 'error' ? 8000 : 3500)
}
function dismiss(id) {
  toasts.value = toasts.value.filter((t) => t.id !== id)
}
function fail(err) {
  notify(toMessage(err), 'error')
}

// 统一包一层：后端每个方法都可能返回可读的中文错误，
// 吞掉它等于让用户对着一个没反应的按钮发呆
async function run(fn, okMsg) {
  try {
    const r = await fn()
    if (okMsg) notify(okMsg)
    return r
  } catch (e) {
    fail(e)
    return undefined
  }
}

// ---------- 载入 ----------

async function loadTools() {
  tools.value = (await api.tools()) || []
}
async function loadWeibo() {
  weibo.value = await api.weiboState()
}
async function loadSettings() {
  settings.value = await api.settings()
}

onMounted(async () => {
  try {
    env.value = await api.env()
    tasks.value = (await api.tasks()) || []
    await Promise.all([loadTools(), loadSettings(), loadWeibo()])
  } catch (e) {
    fail(e)
  }
  EventsOn(PUSH_EVENT, (v) => {
    tasks.value = v || []
  })
})

onUnmounted(() => EventsOff(PUSH_EVENT))

// ---------- 任务 ----------

async function openCreate() {
  taskModal.task = null
  taskModal.open = true
}
async function openEdit(id) {
  const form = await run(() => api.taskForm(id))
  if (!form) return
  taskModal.task = form
  taskModal.open = true
}
async function submitTask(t) {
  const ok = t.id
    ? await run(() => api.updateTask(t), '任务已保存')
    : await run(() => api.addTask(t), '任务已创建')
  if (ok !== undefined) taskModal.open = false
}
async function removeTask(id) {
  const t = tasks.value.find((x) => x.id === id)
  if (!window.confirm(`确定删除任务「${t ? t.name : id}」？`)) return
  await run(() => api.deleteTask(id), '任务已删除')
}
async function loadEvents(id) {
  const list = await run(() => api.taskEvents(id))
  if (list) events[id] = list
}
async function copyText(text) {
  try {
    await navigator.clipboard.writeText(text)
    notify('已复制到剪贴板')
  } catch {
    notify('复制失败，请手动选中链接', 'error')
  }
}

// ---------- 内核 ----------

async function probeTool(id) {
  toolBusy.value = id
  const info = await run(() => api.probeTool(id))
  toolBusy.value = ''
  if (info) {
    notify(`${id}：${info.summary}`)
    await loadTools()
  }
}
async function setToolPath(id) {
  const p = await run(() => api.pickExecutable())
  if (!p) return
  if ((await run(() => api.setToolPath(id, p), '已指定本地文件')) !== undefined) {
    await loadTools()
  }
}
async function resetToolPath(id) {
  if ((await run(() => api.resetToolPath(id), '已恢复默认路径')) !== undefined) {
    await loadTools()
  }
}
async function checkUpdate(id) {
  toolBusy.value = id
  const rel = await run(() => api.checkToolUpdate(id))
  toolBusy.value = ''
  if (rel) {
    releases[id] = rel
    notify(`${id}：${rel.note}`)
  }
}
async function upgradeTool(id) {
  const rel = releases[id]
  const size = rel ? `约 ${rel.sizeMb} MB` : '较大体积'
  if (!window.confirm(`将下载并替换 ${id}（${size}）。期间请勿开播，确定继续？`)) return
  toolBusy.value = id
  const res = await run(() => api.upgradeTool(id), '内核已更新')
  toolBusy.value = ''
  if (res) {
    delete releases[id]
    await loadTools()
  }
}
function openAddTool() {
  toolModal.tool = null
  toolModal.open = true
}
function openEditTool(id) {
  toolModal.tool = tools.value.find((t) => t.id === id) || null
  toolModal.open = true
}
async function submitTool(t) {
  const editing = !!toolModal.tool
  const ok = editing
    ? await run(() => api.editTool(t), '内核已保存')
    : await run(() => api.addTool(t), '内核已添加')
  if (ok !== undefined) {
    toolModal.open = false
    await loadTools()
  }
}
async function removeTool(id) {
  if (!window.confirm(`确定删除内核「${id}」？`)) return
  if ((await run(() => api.deleteTool(id), '内核已删除')) !== undefined) {
    await loadTools()
  }
}

// ---------- 设置 ----------

async function saveSettings() {
  if ((await run(() => api.saveSettings(settings.value), '设置已保存')) !== undefined) {
    await loadSettings()
  }
}
async function pickRecordDir() {
  const d = await run(() => api.pickDirectory())
  if (d) settings.value.recordDir = d
}
async function pickCookieFile() {
  const f = await run(() => api.pickCookieFile())
  if (f) settings.value.youtubeCookieFile = f
}
async function saveCookie(c) {
  weiboBusy.value = true
  const v = await run(() => api.saveWeiboCookie(c), '微博 Cookie 已验证并保存')
  weiboBusy.value = false
  if (v) weibo.value = v
  else await loadWeibo()
}
async function clearCookie() {
  if (!window.confirm('确定清除已保存的微博 Cookie？')) return
  if ((await run(() => api.clearWeiboCookie(), '已清除')) !== undefined) await loadWeibo()
}
async function checkCookie() {
  weiboBusy.value = true
  const v = await run(() => api.checkWeiboCookie())
  weiboBusy.value = false
  if (v) weibo.value = v
}

// ---------- 侧栏 ----------

const runningCount = computed(
  () => tasks.value.filter((t) => ['running', 'starting', 'reconnecting', 'queued'].includes(t.state)).length,
)

const NAV = [
  { key: 'tasks', label: '任务', icon: '▣' },
  { key: 'tools', label: '内核', icon: '◈' },
  { key: 'settings', label: '设置', icon: '⚙' },
]
</script>

<template>
  <div class="flex h-full">
    <!-- 侧边栏 -->
    <aside class="w-52 shrink-0 glass border-r border-line flex flex-col">
      <div class="px-5 pt-5 pb-4 flex items-center gap-2.5" style="--wails-draggable: drag">
        <div class="w-8 h-8 rounded-xl bg-acc/15 border border-acc/30 flex items-center justify-center text-acc font-bold">
          ◤
        </div>
        <div>
          <div class="font-semibold tracking-wide">LiveRelay</div>
          <div class="text-[10px] text-mute">自动直播转发</div>
        </div>
      </div>

      <nav class="px-3 space-y-1 mt-2">
        <button v-for="n in NAV" :key="n.key"
                class="w-full text-left px-3.5 py-2.5 rounded-xl fade border"
                :class="view === n.key
                  ? 'bg-acc/10 text-acc border-acc/20'
                  : 'text-slate-300 hover:bg-ink-700 border-transparent'"
                @click="view = n.key">
          {{ n.icon }}&nbsp;&nbsp;{{ n.label }}
        </button>
      </nav>

      <div class="mt-auto px-4 py-4 border-t border-line text-[11px] text-mute space-y-1">
        <div class="flex justify-between"><span>推流中</span><span class="text-acc">{{ runningCount }}</span></div>
        <div class="flex justify-between"><span>模式</span><span class="text-slate-300">{{ env.mode }}</span></div>
        <div class="flex justify-between"><span>版本</span><span class="text-slate-300">{{ env.version }}</span></div>
      </div>
    </aside>

    <!-- 主区域 -->
    <main class="flex-1 flex flex-col min-w-0">
      <header class="h-11 shrink-0 flex items-center justify-end gap-1 px-3 border-b border-line bg-ink-900/60"
              style="--wails-draggable: drag">
        <div class="no-drag flex gap-1">
          <button class="w-9 h-7 rounded hover:bg-ink-700 text-mute fade" title="最小化"
                  @click="api.minimise()">─</button>
          <button class="w-9 h-7 rounded hover:bg-ink-700 text-mute fade" title="最大化"
                  @click="api.toggleMaximise()">▢</button>
          <button class="w-9 h-7 rounded hover:bg-bad/80 hover:text-white text-mute fade" title="关闭"
                  @click="api.closeWindow()">✕</button>
        </div>
      </header>

      <TasksView v-if="view === 'tasks'" :tasks="tasks" :events="events"
                 @create="openCreate" @edit="openEdit" @remove="removeTask"
                 @start="(id) => run(() => api.startTask(id))"
                 @stop="(id) => run(() => api.stopTask(id))"
                 @open-log="loadEvents" @copy="copyText" />

      <ToolsView v-else-if="view === 'tools'" :tools="tools" :busy="toolBusy" :releases="releases"
                 @add="openAddTool" @edit="openEditTool" @remove="removeTool"
                 @probe="probeTool" @set-path="setToolPath" @reset-path="resetToolPath"
                 @check-update="checkUpdate" @upgrade="upgradeTool" />

      <SettingsView v-else-if="view === 'settings' && settings" :settings="settings" :weibo="weibo"
                    :env="env" :weibo-busy="weiboBusy"
                    @save="saveSettings" @pick-dir="pickRecordDir" @pick-cookie="pickCookieFile"
                    @save-cookie="saveCookie" @clear-cookie="clearCookie" @check-cookie="checkCookie" />
    </main>

    <TaskModal :open="taskModal.open" :task="taskModal.task" :tools="tools" :weibo-usable="weibo.usable"
               @close="taskModal.open = false" @submit="submitTask" />

    <ToolModal :open="toolModal.open" :tool="toolModal.tool" :picker="api.pickExecutable"
               @close="toolModal.open = false" @submit="submitTool" />

    <Toasts :items="toasts" @dismiss="dismiss" />
  </div>
</template>
