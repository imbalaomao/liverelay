// 绑定层的薄封装：把 Wails 生成的方法收拢到一处，
// 并统一把错误转成可以直接显示给用户的中文文案。
//
// 安全约定：这里拿到的任何数据都只经 Vue 的插值渲染（自动转义），
// 全项目不使用 v-html —— 直播源标题、内核帮助文本都是外部输入。
import * as App from '../wailsjs/go/main/App'

/** 把后端错误转成一句能看懂的话。 */
export function toMessage(err) {
  if (!err) return '未知错误'
  if (typeof err === 'string') return err
  return err.message || String(err)
}

export const api = {
  env: () => App.Env(),

  tasks: () => App.Tasks(),
  taskForm: (id) => App.TaskForm(id),
  addTask: (t) => App.AddTask(t),
  updateTask: (t) => App.UpdateTask(t),
  deleteTask: (id) => App.DeleteTask(id),
  startTask: (id) => App.StartTask(id),
  stopTask: (id) => App.StopTask(id),
  taskEvents: (id) => App.TaskEvents(id),

  tools: () => App.Tools(),
  addTool: (t) => App.AddTool(t),
  editTool: (t) => App.EditTool(t),
  deleteTool: (id) => App.DeleteTool(id),
  setToolPath: (id, p) => App.SetToolPath(id, p),
  resetToolPath: (id) => App.ResetToolPath(id),
  probeTool: (id) => App.ProbeTool(id),
  checkToolUpdate: (id) => App.CheckToolUpdate(id),
  upgradeTool: (id) => App.UpgradeTool(id),
  pickExecutable: () => App.PickExecutable(),
  pickDirectory: () => App.PickDirectory(),
  pickCookieFile: () => App.PickCookieFile(),

  settings: () => App.Settings(),
  saveSettings: (s) => App.SaveSettings(s),

  weiboState: () => App.WeiboState(),
  saveWeiboCookie: (c) => App.SaveWeiboCookie(c),
  clearWeiboCookie: () => App.ClearWeiboCookie(),
  checkWeiboCookie: () => App.CheckWeiboCookie(),

  showWindow: () => App.ShowWindow(),
  minimise: () => App.MinimiseWindow(),
  toggleMaximise: () => App.ToggleMaximise(),
  closeWindow: () => App.CloseWindow(),
}
