<script setup>
// 一次性提示条。错误必须让用户看见——后端的报错文案都是写给人看的，
// 吞掉它等于让用户对着一个没反应的按钮发呆。
defineProps({ items: { type: Array, default: () => [] } })
const emit = defineEmits(['dismiss'])
</script>

<template>
  <div class="fixed bottom-5 right-5 z-[60] space-y-2 w-[380px]">
    <div
      v-for="t in items"
      :key="t.id"
      class="fade rounded-xl border px-4 py-3 text-sm shadow-lg glass flex items-start gap-3"
      :class="t.kind === 'error'
        ? 'border-bad/40 text-bad'
        : 'border-acc/30 text-acc'"
    >
      <span class="shrink-0 mt-0.5">{{ t.kind === 'error' ? '✕' : '✓' }}</span>
      <span class="flex-1 break-words selectable text-slate-200">{{ t.text }}</span>
      <button class="shrink-0 text-mute hover:text-white fade" @click="emit('dismiss', t.id)">✕</button>
    </div>
  </div>
</template>
