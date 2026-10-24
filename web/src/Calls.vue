<script setup>
import { startVisiblePolling } from './visible-polling.js'
import InfoTip from './InfoTip.vue'
import { ref, computed, onMounted, onUnmounted } from 'vue'
import { cardFilterOptions, messageSource } from './inbox-cards.js'
import {
  createCallHistory,
  createCallGroups,
  callStates,
  callerLabel,
  ringingLabel,
  groupContainsCall,
} from './call-history.js'
import CallEntry from './CallEntry.vue'
import { markRecordingPlayed, recordingBadge } from './voicemail-listening.js'
import { saveFile } from './download.js'
import { createVoicemailPlayer } from './voicemail-player.js'
import { createVoicemailContext, decodeVoicemail } from './voicemail-audio.js'
const props = defineProps({
  request: Function,
  fileRequest: Function,
  state: Object,
  now: Number,
  linkedId: Number,
  initialCard: String,
})
const emit = defineEmits(['dismiss-linked'])
const rows = ref([]),
  next = ref(0),
  card = ref(props.initialCard || ''),
  search = ref(''),
  error = ref(''),
  linked = ref(null),
  linkedGroup = ref(null),
  expanded = ref({}),
  playback = ref({ id: 0, phase: 'idle' })
const options = computed(() => cardFilterOptions(props.state, props.now))
const visible = computed(() =>
  linkedGroup.value && !rows.value.some((g) => g.id === linkedGroup.value.id)
    ? [linkedGroup.value, ...rows.value]
    : rows.value,
)
let stopped = false,
  loading = false,
  timer,
  linkRevision = 0
const recordingByCall = new Map()
const markingPlayed = new Set()
async function confirmPlayed(call) {
  if (!call?.voicemail_media_id || call.voicemail_played_at || markingPlayed.has(call.id)) return
  markingPlayed.add(call.id)
  try {
    call.voicemail_played_at = await markRecordingPlayed(request, call.voicemail_media_id)
    if (!stopped) await refresh()
  } catch (e) {
    if (!stopped) error.value = '录音已开始播放，但播放标记保存失败：' + e.message
  } finally {
    markingPlayed.delete(call.id)
  }
}
const details = new Map()
const request = (...args) => props.request(...args)
const player = createVoicemailPlayer({
  request: (path, method, body, options) => {
    if (!props.fileRequest) throw new Error('无法读取留言。')
    return props.fileRequest(path, method, body, options)
  },
  decode: decodeVoicemail,
  createContext: createVoicemailContext(),
  onChange(state) {
    playback.value = state
    if (state.phase === 'playing') void confirmPlayed(recordingByCall.get(state.id))
  },
})
const history = createCallGroups({
  request,
  changed: (data) => {
    rows.value = data.items
    next.value = data.next
    const update = data.items.find((g) => g.id === linkedGroup.value?.id)
    if (update) linkedGroup.value = update
  },
})
function date(ts) {
  return ts ? new Date(ts * 1000).toLocaleString('zh-CN', { hour12: false }) : '时间未报告'
}
function entries(group) {
  const data = expanded.value[group.id]?.items || []
  return linked.value &&
    group.id === linkedGroup.value?.id &&
    !data.some((c) => c.id === linked.value.id)
    ? [linked.value, ...data]
    : data
}
function detailHistory(group) {
  if (!details.has(group.id)) {
    details.set(
      group.id,
      createCallHistory({
        request,
        changed: (data) => {
          expanded.value = {
            ...expanded.value,
            [group.id]: { ...expanded.value[group.id], ...data },
          }
          const update = data.items.find((c) => c.id === linked.value?.id)
          if (update) linked.value = update
        },
      }),
    )
  }
  return details.get(group.id)
}
async function loadDetails(group, more = false) {
  const view = expanded.value[group.id]
  if (view?.loading) return
  const controller = detailHistory(group)
  expanded.value = { ...expanded.value, [group.id]: { ...view, loading: true, error: '' } }
  try {
    await controller.load({ group: group.id }, more)
  } catch (e) {
    if (!stopped && details.get(group.id) === controller && expanded.value[group.id])
      expanded.value[group.id].error = e.message
  } finally {
    if (details.get(group.id) === controller && expanded.value[group.id])
      expanded.value[group.id].loading = false
  }
}
async function toggle(group) {
  if (expanded.value[group.id]?.open) {
    expanded.value[group.id].open = false
    return
  }
  expanded.value = { ...expanded.value, [group.id]: { ...expanded.value[group.id], open: true } }
  await loadDetails(group)
}
async function refresh(more = false) {
  if (stopped || loading) return
  loading = true
  try {
    if (await history.load({ card: card.value, q: search.value }, more)) error.value = ''
    await Promise.all(
      visible.value.filter((g) => expanded.value[g.id]?.open).map((g) => loadDetails(g)),
    )
  } catch (e) {
    if (!stopped) error.value = e.message
  } finally {
    loading = false
  }
}
function clearDetails() {
  for (const d of details.values()) d.dispose()
  details.clear()
  expanded.value = {}
}
async function playVoicemail(call) {
  error.value = ''
  try {
    recordingByCall.set(call.id, call)
    await player.toggle(call)
  } catch (e) {
    if (!stopped) error.value = e.message
  }
}
async function recordingChanged(call, problem) {
  if (playback.value.id === call.id) player.stop()
  await refresh()
  if (!stopped && problem) error.value = problem
}
async function downloadVoicemail(call) {
  if (!props.fileRequest || !call?.voicemail_id) return
  error.value = ''
  try {
    const blob = await props.fileRequest('/calls/' + call.id + '/voicemail', 'GET', undefined, {
      types: ['audio/amr'],
      max: 2 * 1024 * 1024,
    })
    saveFile(blob, 'voicemail-' + call.id + '.amr')
  } catch (e) {
    if (!stopped) error.value = e.message
  }
}
async function filter() {
  linked.value = null
  linkedGroup.value = null
  emit('dismiss-linked')
  linkRevision++
  clearDetails()
  const url = new URL(location.href)
  url.searchParams.delete('call')
  window.history.replaceState(null, '', url)
  try {
    if (await history.load({ card: card.value, q: search.value })) error.value = ''
  } catch (e) {
    if (!stopped) error.value = e.message
  }
}
onMounted(async () => {
  timer = startVisiblePolling(refresh, 5000)
  await refresh()
  if (props.linkedId) {
    const version = linkRevision
    try {
      const [call, group] = await Promise.all([
        history.detail(props.linkedId),
        history.groupDetail(props.linkedId),
      ])
      if (!stopped && version === linkRevision) {
        if (!groupContainsCall(group, call))
          throw new Error('通知对应的来电归属不匹配，请重新读取。')
        linked.value = call
        linkedGroup.value = group
        await toggle(group)
      }
    } catch (e) {
      if (!stopped && version === linkRevision) error.value = e.message
    }
  }
})
onUnmounted(() => {
  stopped = true
  linkRevision++
  timer?.()
  player.dispose()
  history.dispose()
  clearDetails()
})
</script>

<template>
  <section class="inbox panel calls-panel">
    <div class="inbox-heading">
      <div>
        <p class="eyebrow">CALLS</p>
        <h2>
          来电记录
          <InfoTip label="来电记录说明"
            >同一卡、同一号码合并展示，展开查看每次来电。默认只记录，不会接听、拒接或拨号；完整未接结束后提醒一次；只有开始时，5分钟后兜底提醒。留言需在录音配置单独开启。录音可使用局域网直连或经
            HTTPS 公网入口上传；已保存的 AMR 可在网页内播放，也可下载。<br />次数包含这张卡的全部已保存来电，逐次记录保留当时卡名和卡槽。匿名或身份未确认的来电单独展示。📼
            标记同组任一次来电有录音；成功开始播放后记为已播放，保存在服务器并跨浏览器同步；下载不改变标记。等待时长以板子上报为准，录音总时长按音频文件计算；Bark
            已接受不等于 iPhone 已读。</InfoTip
          >
        </h2>
      </div>
      <button @click="refresh()">刷新记录</button>
    </div>

    <div v-if="error" class="notice error" role="alert">{{ error }}</div>
    <form class="filters" @submit.prevent="filter">
      <label class="card-filter"
        >来电卡<select v-model="card" @change="filter">
          <option v-for="option in options" :key="option.value" :value="option.value">
            {{ option.value === '' ? '全部来电' : option.label }}
          </option>
        </select></label
      >
      <div class="search">
        <input
          v-model="search"
          type="search"
          maxlength="128"
          placeholder="搜索来电号码或匿名"
        /><button type="submit">搜索</button>
      </div>
    </form>
    <div v-if="!visible.length" class="empty">
      <span class="empty-icon">☎</span>
      <h3>暂无匹配的来电记录</h3>
      <p class="muted">应用连接后收到的来电上报会显示在这里。</p>
    </div>
    <ul v-else class="call-list">
      <li v-for="group in visible" :key="group.id" class="call-group">
        <button
          class="call-summary"
          :aria-expanded="!!expanded[group.id]?.open"
          @click="toggle(group)"
        >
          <div class="detail-head">
            <h3>
              {{ callerLabel(group.latest.phone) }} <span class="count">{{ group.count }} 次</span>
            </h3>
            <span class="call-badges">
              <span
                v-if="recordingBadge(group.voicemail_media_ids?.length, group.unplayed_recordings)"
                class="badge recording-badge"
                >{{
                  recordingBadge(group.voicemail_media_ids?.length, group.unplayed_recordings)
                }}</span
              >
              <span class="badge" :class="{ 'missed-call': group.latest.state === 'missed' }">{{
                callStates[group.latest.state]
              }}</span>
            </span>
          </div>
          <p class="tiny muted">
            {{ messageSource(group.latest, state?.cards) }} · 最近
            {{ date(group.latest.started_at || group.latest.received_at) }}
          </p>
          <div class="call-summary-bottom">
            <span
              ><template v-if="['answered', 'connected'].includes(group.latest.state)"
                >接通前等待 </template
              >{{ ringingLabel(group.latest) }} · {{ group.missed_count }} 次未接</span
            ><span>{{ expanded[group.id]?.open ? '收起记录 ↑' : '查看记录 ↓' }}</span>
          </div>
        </button>
        <div v-if="expanded[group.id]?.open" class="call-group-details">
          <p v-if="expanded[group.id].error" class="notice error" role="alert">
            {{ expanded[group.id].error }}
          </p>
          <p
            v-if="expanded[group.id].loading && !entries(group).length"
            class="tiny muted calls-note"
          >
            正在读取记录…
          </p>
          <CallEntry
            v-for="call in entries(group)"
            :key="call.id"
            :call="call"
            :cards="state?.cards"
            :highlighted="linked?.id === call.id"
            :on-download="downloadVoicemail"
            :on-play="playVoicemail"
            :request="request"
            :on-recording-changed="recordingChanged"
            :playback="playback"
            :recording-label="
              recordingBadge(call.voicemail_id ? 1 : 0, call.voicemail_played_at ? 0 : 1)
            "
          />
          <div v-if="expanded[group.id].next" class="load-more">
            <button :disabled="expanded[group.id].loading" @click="loadDetails(group, true)">
              此号码更早的来电
            </button>
          </div>
        </div>
      </li>
    </ul>
    <div v-if="next" class="load-more"><button @click="refresh(true)">更早的号码记录</button></div>
  </section>
</template>
