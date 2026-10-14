<script setup>
import { ref, nextTick, onMounted, onUnmounted, useId } from 'vue'
const props = defineProps({ label: { type: String, default: '查看说明' } })
const open = ref(false),
  root = ref(null),
  button = ref(null),
  style = ref({})
const id = useId()
async function toggle() {
  open.value = !open.value
  if (!open.value) return
  await nextTick()
  const r = button.value.getBoundingClientRect(),
    width = Math.min(340, window.innerWidth - 24)
  const left = Math.max(12, Math.min(r.left, window.innerWidth - width - 12))
  style.value = {
    position: 'fixed',
    left: `${left}px`,
    width: `${width}px`,
    ...(r.top > window.innerHeight / 2
      ? { bottom: `${window.innerHeight - r.top + 8}px` }
      : { top: `${r.bottom + 8}px` }),
  }
}
function outside(e) {
  if (!root.value?.contains(e.target)) open.value = false
}
function escape(e) {
  if (open.value && e.key === 'Escape') {
    open.value = false
    button.value?.focus()
  }
}
function close() {
  open.value = false
}
onMounted(() => {
  document.addEventListener('pointerdown', outside)
  document.addEventListener('keydown', escape)
  window.addEventListener('resize', close)
  window.addEventListener('scroll', close)
})
onUnmounted(() => {
  document.removeEventListener('pointerdown', outside)
  document.removeEventListener('keydown', escape)
  window.removeEventListener('resize', close)
  window.removeEventListener('scroll', close)
})
</script>
<template>
  <span ref="root" class="info-tip">
    <button
      ref="button"
      type="button"
      class="info-button"
      :aria-label="props.label"
      :aria-expanded="open"
      :aria-controls="id"
      @click.stop.prevent="toggle"
    >
      i
    </button>
    <span v-if="open" :id="id" class="info-content" role="note" :style="style"><slot /></span>
  </span>
</template>
