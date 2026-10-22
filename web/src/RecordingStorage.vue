<script setup>
import { ref, onMounted, onUnmounted } from 'vue'
const props = defineProps({ request: Function })
const stats = ref(null),
  error = ref(''),
  busy = ref(false)
let stopped = false,
  timer
function size(bytes) {
  if (bytes > 0 && bytes < 0.01 * 1024 * 1024) return '<0.01 MiB'
  return (bytes / 1024 / 1024).toFixed(2) + ' MiB'
}
async function load() {
  if (busy.value || stopped) return
  busy.value = true
  try {
    const data = await props.request('/voicemails/storage', 'GET')
    if (![data.files, data.bytes, data.limit_bytes].every((n) => Number.isSafeInteger(n) && n >= 0))
      throw new Error('录音空间统计不完整。')
    if (!stopped) {
      stats.value = data
      error.value = ''
    }
  } catch {
    if (!stopped) error.value = '暂时无法读取录音空间，请刷新重试。'
  } finally {
    busy.value = false
  }
}
onMounted(() => {
  window.addEventListener('simharbor:recording-deleted', load)
  load()
  timer = setInterval(load, 30000)
})
onUnmounted(() => {
  window.removeEventListener('simharbor:recording-deleted', load)
  stopped = true
  clearInterval(timer)
})
</script>
<template>
  <div class="recording-storage" aria-live="polite">
    <div>
      <span class="tiny muted">应用录音存储</span>
      <p v-if="stats">
        <strong>{{ stats.files }} 个文件</strong><strong>{{ size(stats.bytes) }}</strong>
      </p>
      <p v-else class="tiny muted">{{ busy ? '正在读取…' : '尚未读取' }}</p>
      <span class="tiny muted"
        >应用数据目录 voicemail/，包含已关联、未关联和中断遗留文件，不是板子存储。<template
          v-if="stats?.limited"
          >上限 {{ size(stats.limit_bytes) }}，满后拒收，不自动删除。</template
        ></span
      >
      <p v-if="error" class="tiny" role="alert">
        {{ error }}<span v-if="stats">上方为上次读取结果。</span>
      </p>
    </div>
    <button type="button" :disabled="busy" @click="load">刷新空间</button>
  </div>
</template>
