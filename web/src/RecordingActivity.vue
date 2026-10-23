<script setup>
import { startVisiblePolling } from './visible-polling.js'
import {
  createRecordingTrace,
  recordingTraceStages,
  recordingTraceLogNotices,
} from './recording-trace.js'
import { ref, reactive, onMounted, onUnmounted } from 'vue'
const props = defineProps({ request: Function })
const trace = reactive({ id: 0, busy: false, data: null, error: '' })
const flow = createRecordingTrace({
  request: (...args) => props.request(...args),
  changed: (value) => Object.assign(trace, value),
})
function toggleTrace(id) {
  if (trace.id === id) flow.clear()
  else flow.load(id)
}
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
      if (trace.id && !data.items.some((r) => r.call_id === trace.id)) flow.clear()
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
  timer = startVisiblePolling(load, 30000)
})
onUnmounted(() => {
  stopped = true
  flow.dispose()
  timer?.()
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
    <div v-for="item in rows" :key="item.slot" class="tiny recording-activity-row">
      <p>
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
          <a :href="'?call=' + item.call_id">查看来电</a>
          <button
            type="button"
            class="secondary"
            :aria-expanded="trace.id === item.call_id"
            @click="toggleTrace(item.call_id)"
          >
            {{ trace.id === item.call_id ? '收起链路' : '查看链路' }}
          </button></template
        >
        <template v-else>暂无接通上报</template>
      </p>
      <div v-if="item.call_id && trace.id === item.call_id" class="recording-trace">
        <button
          type="button"
          class="secondary"
          :disabled="trace.busy"
          @click="flow.load(item.call_id)"
        >
          刷新链路
        </button>
        <p v-if="trace.busy" role="status">正在读取…</p>
        <p v-if="trace.error" role="alert">{{ trace.error }}</p>
        <template v-if="trace.data">
          <p
            v-for="notice in recordingTraceLogNotices(trace.data)"
            :key="notice"
            class="tiny muted"
          >
            {{ notice }}
          </p>
        </template>
        <dl v-if="trace.data">
          <div v-for="stage in recordingTraceStages(trace.data)" :key="stage.label">
            <dt>{{ stage.label }}</dt>
            <dd>
              {{ stage.text }}<span v-if="stage.at" class="muted"> · {{ date(stage.at) }}</span>
            </dd>
          </div>
        </dl>
        <p class="tiny muted">
          仅按需查看近期证据，无记录不等于失败。来电时间来自板子，诊断时间来自应用；接受命令不代表实际听到，Bark
          接受不代表手机到达。
        </p>
      </div>
    </div>
  </div>
</template>
