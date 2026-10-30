<script setup>
import { ref, reactive, onMounted, onUnmounted } from 'vue'
import InfoTip from './InfoTip.vue'
import DeleteRecording from './DeleteRecording.vue'
import RecordingDuration from './RecordingDuration.vue'
import { callerLabel } from './call-history.js'
import { createUnlinkedVoicemailFlow, unlinkedAudioPath } from './unlinked-voicemail.js'
import { createVoicemailPlayer } from './voicemail-player.js'
import { createVoicemailContext, decodeVoicemail } from './voicemail-audio.js'
import { saveFile } from './download.js'
import { markRecordingPlayed, recordingBadge } from './voicemail-listening.js'
const props = defineProps({ request: Function, fileRequest: Function })
const view = ref({ items: [], next: 0 }),
  loading = ref(false),
  loaded = ref(false),
  error = ref(''),
  playback = ref({ id: 0, phase: 'idle' })
const durationRefresh = ref(0)
let stopped = false
const playingMedia = new Map()
const markingPlayed = reactive(new Set())
const failedRecordings = reactive(new Set())
function badge(item) {
  return recordingBadge(1, item.played_at ? 0 : 1)
}
async function confirmPlayed(item, manual = false) {
  if (!item || item.played_at || markingPlayed.has(item.id)) return
  markingPlayed.add(item.id)
  try {
    item.played_at = await markRecordingPlayed(
      (...args) => props.request(...args),
      item.file_name?.replace(/\.amr$/, ''),
    )
    failedRecordings.delete(item.id)
  } catch (e) {
    if (!stopped)
      error.value =
        (manual ? '播放标记保存失败：' : '录音已开始播放，但播放标记保存失败：') + e.message
  } finally {
    markingPlayed.delete(item.id)
  }
}
function markPlayed(item) {
  error.value = ''
  return confirmPlayed(item, true)
}
const flow = createUnlinkedVoicemailFlow({
  request: (...args) => props.request(...args),
  changed: (data) => (view.value = data),
})
const player = createVoicemailPlayer({
  request: (...args) => props.fileRequest(...args),
  resourceFor: unlinkedAudioPath,
  decode: decodeVoicemail,
  createContext: createVoicemailContext(),
  onChange(state) {
    playback.value = state
    if (state.phase === 'playing') void confirmPlayed(playingMedia.get(state.id))
  },
})
function date(ts) {
  return ts ? new Date(ts * 1000).toLocaleString('zh-CN', { hour12: false }) : '未提供'
}
function playLabel(item) {
  if (playback.value.id !== item.id) return '播放录音'
  return playback.value.phase === 'playing' ? '停止播放' : '取消播放'
}
async function load(more = false) {
  if (loading.value || stopped) return
  loading.value = true
  error.value = ''
  if (!more) player.stop()
  try {
    if (await flow.load(more)) {
      loaded.value = true
      if (!more) durationRefresh.value++
    }
  } catch (e) {
    if (!stopped) error.value = e.message
  } finally {
    loading.value = false
  }
}
async function recordingChanged(problem) {
  await load()
  if (!stopped && problem) error.value = problem
}
async function play(item) {
  error.value = ''
  try {
    playingMedia.set(item.id, item)
    await player.toggle(item)
  } catch (e) {
    if (!stopped) {
      failedRecordings.add(item.id)
      error.value = e.message
    }
  }
}
async function download(item) {
  const path = unlinkedAudioPath(item)
  if (!props.fileRequest || !path) return
  error.value = ''
  try {
    const blob = await props.fileRequest(path, 'GET', undefined, {
      types: ['audio/amr'],
      max: 2 * 1024 * 1024,
    })
    if (!stopped) saveFile(blob, 'unlinked-voicemail-' + item.id + '.amr')
  } catch (e) {
    if (!stopped) error.value = e.message
  }
}
onMounted(() => load())
onUnmounted(() => {
  stopped = true
  flow.dispose()
  player.dispose()
})
</script>
<template>
  <section class="panel compose unlinked-voicemails">
    <div class="detail-head">
      <h2>
        未关联录音
        <InfoTip label="未关联录音说明">
          文件已经保存，但尚未确认属于哪次来电。后续来电上报、重复上传或应用重启时会再次尝试关联。
          可查看、播放、下载或确认删除；不会手动修改关联或发送通知。自动关联后刷新列表，即可从对应来电查看。
        </InfoTip>
      </h2>
      <button type="button" :disabled="loading" @click="load()">
        {{ loading ? '读取中…' : '刷新录音' }}
      </button>
    </div>
    <p class="tiny muted">已保存、尚未对应到某次来电的录音。卡槽按上传信息展示，不推测所属 SIM。</p>
    <p v-if="error" class="notice error" role="alert">{{ error }}</p>
    <p v-if="!loaded && loading" class="empty muted">正在读取录音…</p>
    <p v-else-if="loaded && !loading && !error && !view.items.length" class="empty muted">
      目前没有未关联录音。
    </p>
    <p v-if="view.items.length" class="tiny muted">
      已加载 {{ view.items.length }} 条，按保存顺序从新到旧展示。
    </p>
    <ul v-if="view.items.length" class="unlinked-recording-list">
      <li v-for="item in view.items" :key="item.id" class="call-row">
        <div class="detail-head">
          <h3>录音 #{{ item.id }} · {{ callerLabel(item.phone) }}</h3>
          <span class="call-badges"
            ><span class="badge recording-badge">{{ badge(item) }}</span
            ><span class="badge">未关联</span></span
          >
        </div>
        <dl class="call-times">
          <div>
            <dt>上传卡槽</dt>
            <dd>{{ item.slot ? '卡槽 ' + item.slot : '未提供' }}</dd>
          </div>
          <div>
            <dt>通话开始时间</dt>
            <dd>{{ date(item.started_at) }}</dd>
          </div>
          <div>
            <dt>上传保存时间</dt>
            <dd>{{ date(item.created_at) }}</dd>
          </div>
          <div>
            <dt>文件大小</dt>
            <dd>
              {{ (item.size / 1024).toFixed(1) }} KB ·
              <RecordingDuration
                v-if="item.file_state === 'available'"
                :request="request"
                :media="item.file_name?.replace(/\.amr$/, '')"
                :refresh-key="durationRefresh"
              />
              <span v-else>总时长未知</span>
            </dd>
          </div>
        </dl>
        <p class="tiny muted recording-file-name">文件名：{{ item.file_name || '文件信息异常' }}</p>
        <p v-if="!item.slot || !item.started_at || !item.phone.trim()" class="tiny muted">
          上传信息不完整，可能影响自动关联；缺失项不会按当前卡或接收时间补猜。
        </p>
        <p v-if="item.file_state !== 'available'" class="notice">
          {{
            item.file_state === 'missing'
              ? '索引已保存，录音文件缺失。'
              : '录音文件暂不可读取，或文件信息与索引不一致。'
          }}
          若恢复的备份未包含录音，还需单独恢复 voicemail/ 目录。
        </p>
        <div class="outgoing-actions">
          <button
            type="button"
            :disabled="!fileRequest || item.file_state !== 'available'"
            :aria-pressed="playback.id === item.id"
            @click="play(item)"
          >
            {{ playLabel(item) }}
          </button>
          <button
            type="button"
            :disabled="!fileRequest || item.file_state !== 'available'"
            @click="download(item)"
          >
            下载 AMR
          </button>
          <DeleteRecording
            :request="request"
            :media="item.file_name?.replace(/\.amr$/, '')"
            @changed="recordingChanged"
          />
          <button
            v-if="failedRecordings.has(item.id) && !item.played_at && playback.id !== item.id"
            type="button"
            class="recording-mark-played"
            :disabled="markingPlayed.has(item.id)"
            @click="markPlayed(item)"
          >
            {{ markingPlayed.has(item.id) ? '保存中…' : '标记为已播放' }}
          </button>
        </div>
        <p v-if="playback.id === item.id && playback.warning" class="tiny muted" role="status">
          {{ playback.warning }}
        </p>
      </li>
    </ul>
    <button v-if="view.next" type="button" :disabled="loading" @click="load(true)">
      更早的未关联录音
    </button>
  </section>
</template>
