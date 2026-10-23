<script setup>
import { startVisiblePolling } from './visible-polling.js'
import InfoTip from './InfoTip.vue'
import { checkedRecordingStorage, recordingStorageWarning } from './recording-storage.js'
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
    checkedRecordingStorage(data)
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
  timer = startVisiblePolling(load, 30000)
})
onUnmounted(() => {
  window.removeEventListener('simharbor:recording-deleted', load)
  stopped = true
  timer?.()
})
</script>
<template>
  <div class="recording-storage" aria-live="polite">
    <div>
      <span class="tiny muted">应用录音存储</span>
      <p v-if="stats">
        <strong>{{ stats.complete === false ? '至少 ' : '' }}{{ stats.files }} 个文件</strong
        ><strong
          >{{ stats.complete === false ? '至少 ' : '' }}{{ size(stats.bytes)
          }}<template v-if="stats.limited"> / {{ size(stats.limit_bytes) }}</template></strong
        >
      </p>
      <p v-else class="tiny muted">{{ busy ? '正在读取…' : '尚未读取' }}</p>
      <p v-if="stats?.limited && stats.index_count !== undefined" class="tiny">
        录音接收记录：{{ stats.index_count.toLocaleString() }} /
        {{ stats.index_limit.toLocaleString() }}
        <InfoTip label="录音接收额度说明"
          >包含删除后为去重保留的
          {{ stats.deleted_count }}
          条记录。重复上报不重复占用；删除文件释放空间，不恢复累计记录额度。公网目录文件上限
          {{ stats.file_limit }} 项，包含中断遗留文件。</InfoTip
        >
      </p>
      <p v-if="recordingStorageWarning(stats)" class="notice error" role="status">
        {{ recordingStorageWarning(stats) }}
      </p>
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
