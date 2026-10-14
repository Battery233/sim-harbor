<script setup>
import { ref, watch, onUnmounted } from 'vue'
import InfoTip from './InfoTip.vue'
import { createBoardHistoryFlow } from './board-history-flow.js'
const props = defineProps({ request: Function, online: Boolean })
const kind = ref('sms'),
  slot = ref(1),
  offset = ref(1),
  items = ref([]),
  more = ref(false),
  busy = ref(false),
  error = ref(''),
  queried = ref(false)
const flow = createBoardHistoryFlow({ request: (...args) => props.request(...args) })
watch([kind, slot], () => {
  flow.invalidate()
  items.value = []
  offset.value = 1
  more.value = false
  queried.value = false
  error.value = ''
})
async function load(next = false) {
  if (busy.value) return
  busy.value = true
  error.value = ''
  const start = next ? offset.value + 10 : 1
  try {
    const r = await flow.load(kind.value, slot.value, start)
    if (r) {
      items.value = r.items
      offset.value = r.offset
      more.value = r.has_more
      queried.value = true
    }
  } catch (e) {
    error.value = e.message
    more.value = false
  } finally {
    busy.value = false
  }
}
function date(ts) {
  return ts ? new Date(ts * 1000).toLocaleString('zh-CN') : '未报告'
}
function caller(phone) {
  return phone?.toLowerCase() === 'anonymous' ? '匿名来电' : phone || '号码未提供'
}
onUnmounted(() => flow.dispose())
</script>
<template>
  <div class="board-history">
    <p class="tiny muted">
      只查看，不导入或推送
      <InfoTip label="板子本地历史说明"
        >这些是板子自己的滚动历史，不等于应用的全部记录。历史卡槽不能确定当时的SIM身份；查询不导入、不标已读、不推Bark、不清库。分页时板子库可能变化，不是备份快照。</InfoTip
      >
    </p>
    <form class="settings-grid" @submit.prevent="load(false)">
      <label
        >记录类型<select v-model="kind" :disabled="busy">
          <option value="sms">板子短信</option>
          <option value="calls">板子通话</option>
        </select></label
      >
      <label
        >板子卡槽<select v-model.number="slot" :disabled="busy">
          <option :value="1">卡槽 1</option>
          <option :value="2">卡槽 2</option>
        </select></label
      >
      <button :disabled="busy || !online">{{ busy ? '查询中…' : '查询板子本地历史' }}</button>
    </form>
    <p v-if="error" class="notice error" role="alert">{{ error }}</p>
    <p v-if="queried && !items.length" class="muted">此范围没有板子本地记录</p>
    <article v-for="(row, index) in items" :key="offset + index" class="board-history-row">
      <strong>{{ caller(row.phone) }}</strong
      ><span class="tiny muted">
        ·
        {{
          row.direction === 0
            ? kind === 'sms'
              ? '接收'
              : '来电'
            : kind === 'sms'
              ? '发送'
              : '外呼'
        }}
        · 板子卡槽 {{ row.slot }}</span
      >
      <template v-if="kind === 'sms'"
        ><p class="tiny muted">{{ date(row.timestamp) }}</p>
        <p class="body-text">{{ row.body }}</p></template
      >
      <template v-else
        ><p>开始 {{ date(row.started_at) }} · 结束 {{ date(row.ended_at) }}</p>
        <p>
          {{ row.connected ? '已接通' : '未接通' }} ·
          {{
            row.started_at && row.ended_at >= row.started_at
              ? row.ended_at - row.started_at + ' 秒'
              : '时长未报告'
          }}
        </p></template
      >
    </article>
    <button v-if="more" :disabled="busy || !online" @click="load(true)">查看下一页板子记录</button>
  </div>
</template>
