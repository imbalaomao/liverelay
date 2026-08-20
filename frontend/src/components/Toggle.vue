<script setup>
defineProps({
  modelValue: { type: Boolean, default: false },
  title: { type: String, required: true },
  desc: { type: String, default: '' },
  disabled: { type: Boolean, default: false },
})
const emit = defineEmits(['update:modelValue'])
</script>

<template>
  <label
    class="flex items-center justify-between"
    :class="disabled ? 'opacity-50' : 'cursor-pointer'"
  >
    <div>
      <div class="text-sm">{{ title }}</div>
      <div v-if="desc" class="text-[11px] text-mute mt-0.5">{{ desc }}</div>
    </div>
    <button
      type="button"
      role="switch"
      :aria-checked="modelValue"
      :disabled="disabled"
      class="w-10 h-6 rounded-full p-0.5 fade shrink-0"
      :class="modelValue ? 'bg-acc' : 'bg-ink-600'"
      @click="!disabled && emit('update:modelValue', !modelValue)"
    >
      <div
        class="w-5 h-5 rounded-full bg-white shadow fade"
        :class="modelValue ? 'translate-x-4' : ''"
      />
    </button>
  </label>
</template>
