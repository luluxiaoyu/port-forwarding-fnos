<script setup lang="ts">
import { onMounted, onUnmounted, ref, useTemplateRef } from 'vue'

type Kind = 'error' | 'success'
type Toast = { id: number; kind: Kind; title: string; detail?: string; timer?: number }

const toasts = ref<Toast[]>([])
const region = useTemplateRef<HTMLDivElement>('region')
const canPopover = typeof HTMLElement.prototype.showPopover === 'function'
const latestError = ref<Toast>()
const latestSuccess = ref<Toast>()
let id = 0

onMounted(() => {
  if (canPopover) region.value?.showPopover()
})
onUnmounted(() => toasts.value.forEach(toast => window.clearTimeout(toast.timer)))

function dismiss(toast: Toast) {
  window.clearTimeout(toast.timer)
  toasts.value = toasts.value.filter(item => item.id !== toast.id)
}

function show(kind: Kind, title: string, detail?: string) {
  const existing = toasts.value.find(toast => toast.title === title && toast.detail === detail)
  if (existing) dismiss(existing)
  if (toasts.value.length === 3) dismiss(toasts.value[2])

  const toast: Toast = { id: ++id, kind, title, detail }
  if (kind === 'success') {
    latestSuccess.value = toast
    toast.timer = window.setTimeout(() => dismiss(toast), 3800)
  } else {
    latestError.value = toast
  }
  toasts.value.unshift(toast)
}

defineExpose({ show })
</script>

<template>
  <div ref="region" class="toast-region" :popover="canPopover ? 'manual' : undefined">
    <div class="sr-only" role="alert" aria-atomic="true">
      <span v-if="latestError" :key="latestError.id">{{ latestError.title }} {{ latestError.detail }}</span>
    </div>
    <div class="sr-only" role="status" aria-atomic="true">
      <span v-if="latestSuccess" :key="latestSuccess.id">{{ latestSuccess.title }}</span>
    </div>
    <TransitionGroup tag="ul" name="toast" class="toast-stack" aria-label="操作通知">
      <li v-for="toast in toasts" :key="toast.id" class="toast-card" :data-kind="toast.kind">
        <div class="toast-mark" aria-hidden="true">
          <svg v-if="toast.kind === 'error'" class="icon" viewBox="0 0 24 24">
            <circle cx="12" cy="12" r="9" /><path d="M12 7.5v5.2m0 3.3h.01" />
          </svg>
          <svg v-else class="icon" viewBox="0 0 24 24"><path d="m5 12 4 4L19 6" /></svg>
        </div>
        <div class="toast-content">
          <p>{{ toast.title }}</p>
          <p v-if="toast.detail" class="toast-detail">{{ toast.detail }}</p>
        </div>
        <button type="button" class="toast-close" aria-label="关闭通知" @click="dismiss(toast)">
          <svg class="icon" viewBox="0 0 24 24" aria-hidden="true"><path d="m6 6 12 12M18 6 6 18" /></svg>
        </button>
      </li>
    </TransitionGroup>
  </div>
</template>
