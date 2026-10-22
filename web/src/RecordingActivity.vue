<script setup>
import { ref, onMounted, onUnmounted } from 'vue'
const props = defineProps({ request: Function })
const rows = ref([]),
  error = ref(''),
  busy = ref(false)
let stopped = false,
  timer
function date(value) {
  return new Date(value * 1000).toLocaleString('zh-CN', { hour12: false })
}
async function load() {
  if (busy.value || stopped) return
  busy.value = true
  try {
    const data = await props.request('/voicemails/activity', 'GET')
    if (
      !Array.isArray(data?.items) ||
      data.items.length !== 2 ||
      data.items.some(
        (r, i) =>
          r.slot !== i + 1 ||
          !['call_id', 'started_at', 'connected_at', 'recording_at'].every(
            (k) => Number.isSafeInteger(r[k]) && r[k] >= 0,
          ),
      )
    )
      throw new Error('invalid')
    if (!stopped) {
      rows.value = data.items
      error.value = ''
    }
  } catch {
    if (!stopped) error.value = '暂时无法读取运行记录。'
  } finally {
    busy.value = false
  }
}
onMounted(() => {
  load()
  timer = setInterval(load, 30000)
})
onUnmounted(() => {
  stopped = true
  clearInterval(timer)
})
</script>
<template>
  <div class="recording-activity">
    <div class="detail-head">
      <h3>最近运行记录</h3>
      <button type="button" :disabled="busy" @click="load">刷新运行记录</button>
    </div>
    <p class="tiny muted">仅展示实际收到的接通上报和已保存的关联录音，不能代替板子配置核对。</p>
    <p v-if="error" class="tiny" role="alert">
      {{ error }}<span v-if="rows.length">下方为上次读取结果。</span>
    </p>
    <p v-for="item in rows" :key="item.slot" class="tiny">
      <strong>卡槽 {{ item.slot }}</strong> ·
      <template v-if="item.call_id"
        >最近接通上报 {{ date(item.connected_at) }} ·
        {{ item.ended ? '已收到结束上报' : '结束尚未确认' }} ·
        {{
          item.recording_deleted
            ? '录音已删除'
            : item.recording_at
              ? item.recording_available
                ? '录音可读取 · ' + date(item.recording_at)
                : '录音索引存在，文件不可读取'
              : '暂未收到关联录音'
        }}
        <a :href="'?call=' + item.call_id">查看来电</a></template
      >
      <template v-else>暂无接通上报</template>
    </p>
  </div>
</template>
