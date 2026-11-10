<script setup lang="ts">
import { computed, nextTick, ref, useTemplateRef } from 'vue'

const props = defineProps<{
  id: string
  label: string
  listen?: boolean
  describedBy?: string
}>()
const model = defineModel<string>({ required: true })
const input = useTemplateRef<HTMLInputElement>('input')
const expanded = ref(false)
const active = ref(-1)
const typing = ref(false)

const allAddresses = [
  { value: '*', label: '全部 IPv4 / IPv6', detail: '0.0.0.0 / ::' },
  { value: '0.0.0.0', label: '全部 IPv4', detail: '0.0.0.0' },
  { value: '::', label: '全部 IPv6', detail: '::' },
]
const common = [
  { value: '127.0.0.1', label: '本地回环', detail: '127.0.0.1' },
  { value: null, label: '自定义', detail: props.listen ? '输入 IP 或 localhost' : '输入 IP 或主机名' },
]
const options = computed(() => props.listen ? [...allAddresses, ...common] : common)
const selected = computed(() => options.value.find(option => option.value === model.value))
const displayed = computed(() => typing.value ? model.value : (selected.value?.label ?? model.value))

function focus() {
  input.value?.focus()
}
function close() {
  expanded.value = false
  active.value = -1
}

function openFromInput() {
  if (selected.value && !typing.value) input.value?.select()
  expanded.value = true
}

function toggle() {
  if (expanded.value) close()
  else {
    focus()
    expanded.value = true
  }
}

async function choose(index: number) {
  const option = options.value[index]
  if (option.value === null) {
    if (selected.value) model.value = ''
    typing.value = true
  } else {
    model.value = option.value
    typing.value = false
  }
  close()
  focus()
  // A subsequent typed address replaces the selected preset label.
  await nextTick()
  input.value?.select()
}

function type(event: Event) {
  typing.value = true
  model.value = (event.target as HTMLInputElement).value
  close()
}

function keydown(event: KeyboardEvent) {
  if (event.isComposing) return
  if (event.key === 'ArrowDown' || event.key === 'ArrowUp') {
    event.preventDefault()
    expanded.value = true
    const last = options.value.length - 1
    active.value = event.key === 'ArrowDown'
      ? Math.min(active.value + 1, last)
      : active.value === -1 ? last : Math.max(active.value - 1, 0)
    return
  }
  if (expanded.value && (event.key === 'Escape' || event.key === 'Enter')) {
    event.preventDefault()
    if (event.key === 'Enter' && active.value >= 0) choose(active.value)
    else close()
  }
}

function blur() {
  typing.value = false
  close()
}
</script>

<template>
  <div class="address-field">
    <label :for="id">{{ label }}</label>
    <div class="address-combo" :data-open="expanded">
      <input :id="id" ref="input" role="combobox" type="text" required
        :value="displayed" :maxlength="listen ? 45 : 254"
        autocomplete="off" spellcheck="false"
        :placeholder="listen ? '选择或输入地址' : '选择或输入服务地址'"
        aria-autocomplete="none" :aria-expanded="expanded" :aria-controls="id + '-options'"
        :aria-activedescendant="expanded && active >= 0 ? id + '-option-' + active : undefined"
        :aria-describedby="describedBy"
        @focus="selected && input?.select()" @click="openFromInput" @blur="blur" @input="type" @keydown="keydown" />
      <button type="button" class="combo-toggle" tabindex="-1" :aria-label="'选择' + label"
        @pointerdown.prevent @click="toggle">
        <svg class="icon" viewBox="0 0 24 24" aria-hidden="true"><path d="m6 9 6 6 6-6" /></svg>
      </button>
      <Transition name="combo">
      <ul v-show="expanded" :id="id + '-options'" role="listbox" :aria-label="label + '选项'"
        :aria-hidden="!expanded" class="combo-options">
        <li v-for="(option, index) in options" :id="id + '-option-' + index" :key="option.label"
          role="option" :aria-selected="active === index || (active < 0 && option.value === model)" class="combo-option"
          @pointerdown.prevent @click="choose(index)" @mouseenter="active = index">
          <svg v-if="option.value === model" class="icon combo-check" viewBox="0 0 24 24" aria-hidden="true">
            <path d="m5 12 4 4L19 6" />
          </svg>
          <span class="combo-option-label">{{ option.label }}</span>
          <span class="combo-detail">{{ option.detail }}</span>
        </li>
      </ul>
      </Transition>
    </div>
  </div>
</template>
