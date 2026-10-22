<script setup>
import { ref, reactive, computed, onMounted, onUnmounted, nextTick } from 'vue'
import { slotView, mergeMessages, messageText, hasFormattedText, wifiName } from './presentation.js'
import { cardFilterOptions, messageSource } from './inbox-cards.js'
import { multipartProgress } from './multipart-progress.js'
import { createInboxDeletion } from './inbox-delete.js'
import CardRecords from './CardRecords.vue'
import InfoTip from './InfoTip.vue'
import CardPushControls from './CardPushControls.vue'
import RecordingSettings from './RecordingSettings.vue'
import SystemSettings from './SystemSettings.vue'
import { settingsErrors } from './settings-flow.js'
import Outbox from './Outbox.vue'
import Calls from './Calls.vue'
import Notifications from './Notifications.vue'
import Diagnostics from './Diagnostics.vue'
import DataTools from './DataTools.vue'
import { createFileRequest } from './download.js'
import { backupErrors } from './backup-errors.js'
import { createRequest } from './request.js'
import { createAccessSession } from './access-session.js'
import { createAuthHeaders } from './auth-config.js'
const tab = ref('inbox')
const linkedCall = ref(0)
const callsCardFilter = ref('')

const state = ref(null),
  items = ref([]),
  selected = ref(null)
const cardFilter = ref(''),
  search = ref(''),
  next = ref(0)
const loading = ref(false),
  querying = ref(false),
  error = ref(''),
  note = ref('')
const unlocking = ref(false)
const showRaw = ref(false)
const access = createAccessSession()
const authHeaders = createAuthHeaders()
const locked = ref(false),
  token = ref(''),
  now = ref(Date.now() / 1000),
  dialog = ref(null)
let previousFocus
let credential = access.read(),
  interval,
  clock,
  generation = 0,
  stateRevision = 0,
  messageRevision = 0,
  queryRevision = 0,
  stopped = false
const errors = {
  ...backupErrors,
  ...settingsErrors,
  invalid_board_action: '不支持此操作，或心跳值超过安全范围；请先核对参数。',
  voicemail_disabled: '应用接收服务尚未开启。请在“录音配置”保存设置并重启应用。',
  voicemail_sms_required:
    '这块板子需要用短信修改接听方式。请填写对应卡槽自己的号码和板子短信指令用户密码，再确认费用。',
  sms_cost_required: '请先勾选设置短信的费用确认。',
  stop_auto_answer_required: '请勾选“我确认停止板子自动接听”，再确认执行。',
  control_already_dispatched: '这条设置短信可能已经发出。请先读取设置，不要重复发送。',
  card_number_mismatch: '填写的号码和这块卡当前的本机号码不一致，已阻止发送。',
  voicemail_sms_rejected:
    '板子没有接受短信指令。请核对用户密码；密码错误时 30 分钟内可能不再回复。上传地址可能已经写入，请重新读取后再确认。',
  voicemail_delete_failed: '录音删除未完成，请刷新列表核对，不会自动重试。',
  voicemail_cleanup_pending:
    '删除已登记，但文件清理未完成。请查看运行诊断；下次启动会重试已登记的清理。',
  voicemail_delete_confirmation_required: '请先确认要删除的这段录音。',
  voicemail_source_rejected: '留言上传来源不在设备允许范围内。',
  voicemail_upload_rejected: '留言上传未通过校验。',
  board_parameter_changed: '板子的当前值已变化，请重新查询后再确认。',
  board_reply_invalid: '板子返回的参数无法识别，保留当前设置。',
  board_setting_unknown: '板子修改结果未确认，可能已经生效。',
  notifications_disabled: '通知未启用，请先配置 Bark 并重启应用。',
  notification_confirmation_required: '请确认接收端与通知操作。',
  notification_retry_unavailable: '任务已处理、到达尝试时间或刚请求过重试，请刷新查看。',
  notification_test_cooldown: '距离上次连接测试不足一分钟，请查看原结果。',
  push_request_failed: '连接测试未被确认接受，请查看诊断结果并核对 iPhone。',
  outbound_disabled: '外发尚未启用。完成真机与资费确认后再开启。',
  outbound_restore_blocked: '恢复保护尚未解除，请到数据维护完成迁移确认。',
  migration_consent_required: '请逐项核对迁移条件并确认。',
  migration_backup_required: '请先保存到服务器或下载当前数据库备份，再于15分钟内预览。',
  migration_state_changed: '迁移或发送历史已变化，请读取状态并重新备份、预览。',
  migration_preview_expired: '预览已过期或失效，请读取状态后重新预览。',
  migration_in_progress: '备份或迁移确认正在处理，请稍后读取状态。',
  invalid_sms: '请检查目标号码、卡槽与正文。',
  segment_limit: '正文超过当前允许的短信段数，请缩短后重新预览。',
  duplicate_content: '同一张卡、号码和正文已有操作，已阻止重复发送。',
  operation_conflict: '原操作内容不能修改，请查看保存的记录。',
  confirmation_required: '目标号码、卡槽或费用确认与原预览不一致。',
  preview_expired: '预览已过期或取消，未下发的预览可重新准备。',
  card_changed: 'SIM 卡身份已变化，已阻止发送。',
  card_identity_required: '板子未报告 SIM 卡身份，已阻止发送。',
  card_not_ready: '卡槽当前未就绪，已阻止发送。',
  card_state_stale: '卡槽查询结果已过期，已阻止发送。',
  device_connection_changed: '板子连接已变化，已阻止本次发送。',
  send_in_progress: '另一条确认正在处理，请查看原操作记录。',
  send_interval: '尚未达到这张卡的发送间隔，已阻止发送。',
  daily_limit: '这张卡今日尝试次数已达上限，已阻止发送。',
  already_dispatched: '操作可能已下发，不能取消或重发。',
  device_offline: '板子当前未连接。',
  query_in_progress: '状态查询正在进行，请稍后。',
  device_result_unknown: '没有等到板子的结果，请查看最近上报时间。',
  device_query_failed: '板子未完成状态查询。',
  device_write_failed: '连接中断，查询未完成。',
  not_found: '这条短信不存在或已删除。',
  delete_confirmation_required: '请先核对短信，再确认删除。',
  invalid_filter: '检索条件无效。',
  host_rejected: '本机免凭据入口请使用 localhost 或回环地址。',
  origin_rejected: '访问来源不匹配，请使用管理页面地址。',
  database_unavailable: '暂时无法读取存储。',
}
const api = createRequest({
  getCredential: () => credential,
  getAuthHeaders: (value) => authHeaders.get(value),
  onUnauthorized: lockAccess,
  errors,
})
const deleteState = reactive({ target: null, busy: false, error: '' })
const deleteCancel = ref(null)
const deletion = createInboxDeletion({
  state: deleteState,
  api,
  session: () => generation,
  onDeleted: async (id) => {
    messageRevision++
    items.value = items.value.filter((m) => m.id !== id)
    closeMessage()
    await refresh(true)
    note.value = '短信已删除。'
  },
})
async function askDelete() {
  if (deletion.request(selected.value)) {
    await nextTick()
    deleteCancel.value?.focus()
  }
}
function escapeDetail() {
  if (deleteState.target) deletion.cancel()
  else closeMessage()
}
const fileRequest = createFileRequest({
  getCredential: () => credential,
  getAuthHeaders: (value) => authHeaders.get(value),
  onUnauthorized: lockAccess,
})
const online = computed(
  () => state.value?.online && now.value - state.value.last_seen <= state.value.stale_seconds,
)
const cards = computed(() =>
  [1, 2].map((n) => ({
    ...slotView({ ...state.value, online: online.value }, n, now.value),
    slot: n,
  })),
)
function cardPushChanged(result) {
  if (result?.card_id && state.value?.cards) {
    state.value = {
      ...state.value,
      cards: state.value.cards.map((c) =>
        c.id === result.card_id ? { ...c, notification_enabled: result.notification_enabled } : c,
      ),
    }
  }
  refresh(false, true)
}
const inboxOptions = computed(() => cardFilterOptions(state.value, now.value))
const wifi = computed(() => state.value?.reported?.wifi || {})
function date(ts) {
  return ts ? new Date(ts * 1000).toLocaleString('zh-CN', { hour12: false }) : '尚未收到上报'
}
function relative(ts) {
  if (!ts) return '等待板子接入'
  const n = Math.max(0, Math.floor(now.value - ts))
  return n < 60
    ? `${n} 秒前`
    : n < 3600
      ? `${Math.floor(n / 60)} 分钟前`
      : `${Math.floor(n / 3600)} 小时前`
}
async function loadState() {
  const version = generation,
    revision = ++stateRevision
  const data = await api('/state')
  if (version === generation && revision === stateRevision && !stopped) state.value = data
}
async function loadMessages(reset = false, more = false) {
  const version = generation,
    revision = ++messageRevision,
    params = new URLSearchParams({ card: cardFilter.value, q: search.value.trim() })
  if (more) params.set('before', next.value)
  const data = await api('/messages?' + params)
  if (version !== generation || revision !== messageRevision || stopped) return
  items.value = reset ? data.items : mergeMessages(items.value, data.items)
  if (reset || more || (!next.value && items.value.length <= 50)) next.value = data.next_before
  if (selected.value)
    selected.value = items.value.find((m) => m.id === selected.value.id) || selected.value
}
async function refresh(reset = false, force = false) {
  if (stopped || (loading.value && !force)) return false
  const version = generation
  loading.value = true
  try {
    await Promise.all([loadState(), loadMessages(reset)])
    if (version !== generation || stopped) return false
    error.value = ''
    return true
  } catch (e) {
    if (version === generation && !stopped) error.value = e.message
    return false
  } finally {
    if (version === generation && !stopped) loading.value = false
  }
}
async function filter() {
  const version = ++generation
  loading.value = true
  try {
    await loadMessages(true)
    if (version === generation && !stopped) error.value = ''
  } catch (e) {
    if (version === generation && !stopped) error.value = e.message
  } finally {
    if (version === generation && !stopped) loading.value = false
  }
}
async function viewCardInbox(id) {
  cardFilter.value = id
  tab.value = 'inbox'
  await filter()
}
function viewCardCalls(id = '') {
  if (id && !state.value?.cards?.some((card) => card.id === id)) return
  callsCardFilter.value = id
  linkedCall.value = 0
  const url = new URL(location.href)
  url.searchParams.delete('call')
  window.history.replaceState(null, '', url)
  tab.value = 'calls'
}
async function more() {
  if (loading.value || stopped) return
  const version = generation
  loading.value = true
  try {
    await loadMessages(false, true)
  } catch (e) {
    if (version === generation && !stopped) error.value = e.message
  } finally {
    if (version === generation && !stopped) loading.value = false
  }
}
async function openMessage(m) {
  const version = generation
  previousFocus = document.activeElement
  deletion.reset()
  selected.value = m
  showRaw.value = false
  note.value = ''
  await nextTick()
  if (version !== generation || stopped || selected.value?.id !== m.id) return
  dialog.value?.focus()
  const u = new URL(location.href)
  u.searchParams.set('message', m.id)
  history.replaceState(null, '', u)
  if (!m.read)
    try {
      await api(`/messages/${m.id}/read`, 'POST')
      if (version !== generation || stopped) return
      m.read = true
      if (selected.value?.id === m.id) selected.value = { ...m, read: true }
    } catch (e) {
      if (version === generation && !stopped) error.value = e.message
    }
}
async function query() {
  if (querying.value || stopped || locked.value) return
  const version = generation,
    revision = ++queryRevision
  querying.value = true
  try {
    await api('/state/query', 'POST')
    await loadState()
    if (version !== generation || stopped) return
    error.value = ''
    note.value = '已获取最新状态。'
  } catch (e) {
    if (version === generation && !stopped) error.value = e.message
  } finally {
    if (revision === queryRevision && !stopped) querying.value = false
  }
}
async function copy() {
  try {
    await navigator.clipboard.writeText(messageText(selected.value, showRaw.value))
    note.value = showRaw.value ? '原文已复制。' : '正文已复制。'
  } catch {
    note.value = '当前浏览器无法自动复制，请长按正文选择复制。'
  }
}
async function unlock() {
  if (unlocking.value || stopped) return
  authHeaders.reset()
  const version = ++generation
  unlocking.value = true
  credential = token.value.trim()
  token.value = ''
  try {
    const verified = await refresh(true, true)
    if (!verified || version !== generation || stopped) return
    note.value = access.save(credential) ? '' : '浏览器未允许保存凭据；刷新页面后需要重新输入。'
    locked.value = false
    await openLinked()
  } finally {
    unlocking.value = false
  }
}
async function openLinked() {
  const version = generation
  const callId = Number(new URL(location.href).searchParams.get('call'))
  if (Number.isSafeInteger(callId) && callId > 0 && !locked.value) {
    linkedCall.value = callId
    callsCardFilter.value = ''
    tab.value = 'calls'
    return
  }
  const id = new URL(location.href).searchParams.get('message')
  if (id && /^\d+$/.test(id) && !locked.value)
    try {
      const m = await api('/messages/' + id)
      if (version === generation && !stopped && !locked.value) await openMessage(m)
    } catch (e) {
      if (version === generation && !stopped) error.value = e.message
    }
}
function forget() {
  authHeaders.reset()
  deletion.reset()
  generation++
  queryRevision++
  credential = ''
  access.clear()
  locked.value = true
  items.value = []
  selected.value = null
  state.value = null
  next.value = 0
  loading.value = false
  querying.value = false
  note.value = ''
  error.value = ''
}
function lockAccess() {
  forget()
  error.value = '请输入有效的管理访问凭据。'
}
function closeMessage() {
  if (deleteState.busy) return
  deletion.reset()
  selected.value = null
  previousFocus?.focus()
  const u = new URL(location.href)
  u.searchParams.delete('message')
  history.replaceState(null, '', u)
}
onMounted(async () => {
  interval = setInterval(() => {
    if (!locked.value) refresh()
  }, 15000)
  clock = setInterval(() => (now.value = Date.now() / 1000), 1000)
  await refresh(true)
  if (!stopped) await openLinked()
})
onUnmounted(() => {
  stopped = true
  clearInterval(interval)
  clearInterval(clock)
})
</script>

<template>
  <div class="shell">
    <header class="topbar">
      <a class="brand" href="/" aria-label="SIMHarbor 首页"
        ><span class="mark">S</span><strong>SIMHarbor</strong
        ><span class="subtitle">双卡管理</span></a
      >
      <div class="top-actions">
        <span v-if="state?.version" class="tiny muted">v{{ state.version }}</span>
        <span class="env">{{
          state ? (state.environment === 'cloud' ? '云端' : '本地') : '管理入口'
        }}</span
        ><button v-if="credential" class="quiet" @click="forget">退出访问</button>
      </div>
    </header>
    <main>
      <div class="page-heading">
        <div>
          <p class="eyebrow">MY SIM BOARD</p>
          <h1>两张卡，一个管理台。</h1>
          <p class="muted">集中管理短信、来电、卡状态与通知。</p>
        </div>
        <button @click="refresh(true)" :disabled="loading || locked">
          {{ loading ? '正在刷新…' : '刷新收件' }}
        </button>
      </div>
      <div v-if="error && !locked" class="notice error" role="alert">{{ error }}</div>
      <div v-if="note" class="notice" role="status">{{ note }}</div>
      <section v-if="locked" class="access panel">
        <h2>管理访问</h2>
        <p class="muted">输入为当前服务配置的访问凭据。本地仅在这个浏览器会话中保存。</p>
        <p v-if="error" class="notice error" role="alert">{{ error }}</p>
        <form @submit.prevent="unlock">
          <input
            type="password"
            v-model="token"
            required
            autocomplete="off"
            aria-label="管理访问凭据"
          /><button class="primary" :disabled="unlocking">
            {{ unlocking ? '正在验证…' : '进入管理' }}
          </button>
        </form>
      </section>
      <template v-else>
        <section class="overview panel">
          <div class="connection">
            <span class="status-dot" :class="{ good: online }"></span>
            <div>
              <h2>{{ online ? '板子已连接' : '等待板子连接' }}</h2>
              <p class="muted">最后上报 · {{ relative(state?.last_seen) }}</p>
            </div>
          </div>
          <div class="overview-right">
            <span class="muted">{{ state?.device_id || '—' }}</span
            ><button class="quiet" @click="query" :disabled="!online || querying">
              {{ querying ? '查询中…' : '查询板子状态' }}
            </button>
          </div>
        </section>
        <div class="cards">
          <section v-for="card in cards" :key="card.slot" class="sim-card panel">
            <div class="card-head">
              <span class="sim-icon">SIM {{ card.slot }}</span
              ><span class="badge" :class="{ success: card.good }">{{ card.label }}</span>
            </div>
            <h2>{{ card.name }}</h2>
            <p class="sim-number">
              <strong>{{ card.number || '未记录号码' }}</strong>
            </p>
            <p v-if="card.savedFields.length" class="tiny muted">
              {{ card.savedFields.join('、') }}使用已保存资料
            </p>
            <dl>
              <div>
                <dt>{{ card.fresh ? '信号' : '上次信号' }}</dt>
                <dd>{{ card.signal }}</dd>
              </div>
              <div>
                <dt>注册网络</dt>
                <dd class="registered-network">
                  {{ card.operator }}
                  <span
                    v-if="card.operatorCode && card.operator !== card.operatorCode"
                    class="tiny muted network-code"
                    >{{ card.operatorCode }}</span
                  >
                  <span v-if="card.crossBorderRoaming" class="tiny roaming-label">跨境漫游</span>
                </dd>
              </div>
            </dl>
            <p class="tiny muted sim-query-time">
              上次查询 ·
              {{ state?.status_received_at ? date(state.status_received_at) : '尚未查询' }}
              <span v-if="!card.fresh"> · {{ online ? '自动更新中' : '连接后自动更新' }}</span>
            </p>
          </section>
          <section class="push-card panel">
            <div class="card-head">
              <span class="sim-icon">BARK</span
              ><span class="badge" :class="{ success: state?.bark_enabled }">{{
                state?.bark_enabled ? '已配置' : '未配置'
              }}</span>
            </div>
            <h2>
              iPhone 通知
              <InfoTip label="通知统计说明">
                成功表示 Bark 已接受，不代表 iPhone 已读。按{{
                  state?.notification_success?.timezone === 'Asia/Shanghai'
                    ? '北京时间'
                    : state?.notification_success?.timezone || '配置时区'
                }}自然日统计，近30天含今天，包含短信和未接来电。另有{{
                  state?.notification_success?.undated || 0
                }}条旧成功未记录日期，未计入统计。通知任务单独保存，推送失败不会丢失短信。
              </InfoTip>
            </h2>
            <dl class="push-metrics">
              <div>
                <dt>待推送</dt>
                <dd>{{ state?.notifications_pending || 0 }}</dd>
              </div>
              <div>
                <dt>等待重试</dt>
                <dd>{{ state?.notifications_retrying || 0 }}</dd>
              </div>
              <div>
                <dt>今日成功</dt>
                <dd>{{ state?.notification_success?.today ?? '—' }}</dd>
              </div>
              <div>
                <dt>近 30 天成功</dt>
                <dd>{{ state?.notification_success?.last_30_days ?? '—' }}</dd>
              </div>
            </dl>
            <p class="tiny muted">
              持续异常提醒 · {{ state?.status_alerts?.enabled ? '已开启' : '未开启' }}
              <InfoTip label="持续异常说明"
                >离线15分钟、SIM注册异常30分钟，各提醒一次；恢复稳定5分钟提醒一次。应用停机无法主动提醒，重启后不补推停机期间故障。管理应用的通知设置可关闭此功能。</InfoTip
              >
            </p>
            <CardPushControls :state="state" :request="api" @changed="cardPushChanged" />
          </section>
        </div>
        <div class="network-line">
          <span>上次查询 · WiFi {{ wifi.mode || '状态未知' }}</span
          ><span>设备型号 / 固件 · {{ state?.reported?.hwVer || '尚未查询' }}</span
          ><span v-if="wifi.ssid">{{ wifiName(wifi.ssid) }}</span>
        </div>
        <nav class="box-tabs" aria-label="管理功能">
          <button :class="{ primary: tab === 'inbox' }" @click="tab = 'inbox'">收件箱</button>
          <button :class="{ primary: tab === 'outbox' }" @click="tab = 'outbox'">发件箱</button>
          <button :class="{ primary: tab === 'calls' }" @click="viewCardCalls()">来电记录</button>
          <button :class="{ primary: tab === 'notifications' }" @click="tab = 'notifications'">
            短信与通话推送
          </button>
          <button :class="{ primary: tab === 'diagnostics' }" @click="tab = 'diagnostics'">
            运行诊断
          </button>
          <button :class="{ primary: tab === 'data' }" @click="tab = 'data'">数据维护</button>
          <button :class="{ primary: tab === 'cards' }" @click="tab = 'cards'">卡记录</button>
          <button :class="{ primary: tab === 'recording' }" @click="tab = 'recording'">
            录音配置
          </button>
          <button :class="{ primary: tab === 'settings' }" @click="tab = 'settings'">
            系统配置
          </button>
        </nav>
        <CardRecords
          v-if="tab === 'cards'"
          :state="state"
          :now="now"
          @view-inbox="viewCardInbox"
          @view-calls="viewCardCalls"
        />
        <DataTools
          v-if="tab === 'data'"
          :file-request="fileRequest"
          :request="api"
          :state="state"
          @changed="refresh(false)"
        />
        <Outbox
          v-if="tab === 'outbox' && state"
          :request="api"
          :state="state"
          :online="online"
          :now="now"
        />
        <Calls
          v-if="tab === 'calls' && state"
          :key="callsCardFilter"
          :request="api"
          :file-request="fileRequest"
          :state="state"
          :now="now"
          :linked-id="linkedCall"
          :initial-card="callsCardFilter"
          @dismiss-linked="linkedCall = 0"
        />
        <Notifications v-if="tab === 'notifications'" :request="api" />
        <Diagnostics v-if="tab === 'diagnostics'" :request="api" :state="state" />
        <SystemSettings v-if="tab === 'settings'" :request="api" :state="state" />
        <RecordingSettings
          v-if="tab === 'recording'"
          :request="api"
          :file-request="fileRequest"
          :state="state"
          @changed="refresh(false)"
        />
        <section v-show="tab === 'inbox'" class="inbox panel">
          <div class="inbox-heading">
            <div>
              <p class="eyebrow">MESSAGES</p>
              <h2>
                收件箱 <span class="count">{{ items.length }}</span>
              </h2>
            </div>
            <span class="tiny muted">云端与本地使用各自的历史库</span>
          </div>
          <form class="filters" @submit.prevent="filter">
            <label class="card-filter"
              >收件卡<select v-model="cardFilter" @change="filter">
                <option v-for="option in inboxOptions" :key="option.value" :value="option.value">
                  {{ option.label }}
                </option>
              </select></label
            >
            <div class="search">
              <input
                v-model="search"
                type="search"
                placeholder="搜索正文或发送方"
                aria-label="搜索正文或发送方"
                maxlength="256"
              /><button :disabled="loading">搜索</button>
            </div>
          </form>
          <p class="tiny muted inbox-note">
            按卡身份查看历史，换槽不会改变归属。位置过期时请查询板子状态；旧记录不按当前卡猜身份。
          </p>
          <div v-if="!items.length" class="empty">
            <span class="empty-icon">✉</span>
            <h3>{{ search || cardFilter !== '' ? '没有符合条件的短信' : '还没有收到短信' }}</h3>
            <p class="muted">
              {{
                search || cardFilter !== ''
                  ? '试试其他检索条件。'
                  : '板子接入后，短信会在这里持续保存。'
              }}
            </p>
          </div>
          <ul v-else class="messages">
            <li v-for="m in items" :key="m.id">
              <button class="message-row" @click="openMessage(m)">
                <span class="mini-sim" :title="'收件时卡槽 ' + m.slot">{{ m.slot }}</span>
                <div class="message-main">
                  <div class="message-title">
                    <strong>{{ m.phone }}</strong
                    ><span v-if="!m.read" class="unread-dot" aria-label="未读"></span
                    ><span v-if="multipartProgress(m)" class="badge small">{{
                      multipartProgress(m).label
                    }}</span>
                  </div>
                  <div class="tiny muted">
                    {{ messageSource(m, state?.cards) }} · 收件时卡槽 {{ m.slot
                    }}<span v-if="!m.card_id"> · 身份未确认</span>
                  </div>
                  <p>{{ messageText(m) || '长短信正在等待完整内容…' }}</p>
                </div>
                <time :datetime="new Date(m.timestamp * 1000).toISOString()">{{
                  date(m.timestamp)
                }}</time
                ><span class="arrow">›</span>
              </button>
            </li>
          </ul>
          <div v-if="next" class="load-more">
            <button :disabled="loading" @click="more">
              {{ loading ? '加载中…' : '加载更早的短信' }}
            </button>
          </div>
        </section>
        <p class="footer-note">
          连接状态与卡槽状态分别更新。短信时间由板子上报；推送被服务接受不等于已在 iPhone 阅读。
        </p>
      </template>
    </main>
    <div
      v-if="selected"
      class="modal-backdrop"
      @click.self="closeMessage"
      @keydown.esc="escapeDetail"
    >
      <section
        class="detail panel"
        role="dialog"
        aria-modal="true"
        aria-labelledby="detail-title"
        tabindex="-1"
        ref="dialog"
      >
        <div class="detail-head">
          <span class="badge">
            {{ messageSource(selected, state?.cards) }} · 收件时卡槽 {{ selected.slot }}</span
          ><button
            class="quiet"
            :disabled="deleteState.busy"
            @click="closeMessage"
            aria-label="关闭短信详情"
          >
            关闭
          </button>
        </div>
        <h2 id="detail-title">{{ selected.phone }}</h2>
        <p class="tiny muted">
          {{ date(selected.timestamp) }} · {{ selected.read ? '已读' : '未读' }}
        </p>
        <p v-if="multipartProgress(selected)" class="notice">
          {{ multipartProgress(selected).detail }}
        </p>
        <p v-if="hasFormattedText(selected)" class="tiny muted">
          {{ showRaw ? '正在查看收到的原文。' : '已按换行和缩进排版，原文保留。' }}
        </p>
        <p class="body-text">{{ messageText(selected, showRaw) || '等待更多分片…' }}</p>
        <button
          v-if="hasFormattedText(selected)"
          @click="showRaw = !showRaw"
          :aria-pressed="showRaw"
        >
          {{ showRaw ? '恢复排版' : '查看原文' }}
        </button>
        <div v-if="!deleteState.target" class="detail-actions">
          <button @click="copy">复制正文</button>
          <button class="danger" @click="askDelete">删除短信</button>
        </div>
        <section v-else class="delete-confirmation" aria-label="确认删除短信">
          <h3>删除这条短信？</h3>
          <p>
            {{ messageSource(deleteState.target, state?.cards) }} · {{ deleteState.target.phone }}
          </p>
          <p class="tiny muted">{{ date(deleteState.target.timestamp) }}</p>
          <p class="delete-preview">
            {{ messageText(deleteState.target).slice(0, 120) || '尚未收齐的长短信' }}
          </p>
          <p class="tiny muted">
            从本应用收件箱删除，不能直接撤销。板子、已有备份和 Bark 历史不受影响。
          </p>
          <p v-if="deleteState.error" class="notice" role="alert">{{ deleteState.error }}</p>
          <div class="detail-actions">
            <button ref="deleteCancel" :disabled="deleteState.busy" @click="deletion.cancel">
              取消
            </button>
            <button class="danger" :disabled="deleteState.busy" @click="deletion.confirm">
              {{ deleteState.busy ? '删除中…' : '确认删除这条短信' }}
            </button>
          </div>
        </section>
      </section>
    </div>
  </div>
</template>
