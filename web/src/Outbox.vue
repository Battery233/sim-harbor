<script setup>
import InfoTip from './InfoTip.vue'
import { ref, computed, onMounted, onUnmounted } from 'vue'
import { createOutboxFlow, phaseLabels, deliveryLabels } from './outbox-flow.js'
import {
  createOutboxHistory,
  outgoingFilterOptions,
  outgoingCardLabel,
  sendSlotOptions,
} from './outbox-history.js'
const props = defineProps({ request: Function, state: Object, online: Boolean, now: Number })
const cardFilter = ref('')
const filterOptions = computed(() => outgoingFilterOptions(props.state, props.now))
const slotOptions = computed(() => sendSlotOptions(props.state, props.now))
const rows = ref([]),
  next = ref(0),
  current = ref(null),
  pending = ref(''),
  attempted = ref(false)
const slot = ref('1'),
  phone = ref(''),
  body = ref(''),
  retyped = ref(''),
  consent = ref(false)
const busy = ref(false),
  error = ref(''),
  flow = ref(null)
let timer,
  stopped = false,
  polling = false
const history = createOutboxHistory({
  request: (...args) => props.request(...args),
  changed: (data) => {
    rows.value = data.items
    next.value = data.next
  },
})
function sync() {
  pending.value = flow.value?.id || ''
  current.value = flow.value?.record || null
  attempted.value = flow.value?.attempted || false
}
function resetConsent() {
  retyped.value = ''
  consent.value = false
}
const units = computed(() => body.value.trim().length)
const estimated = computed(() => (units.value <= 70 ? 1 : Math.ceil(units.value / 67)))
const canPreview = computed(
  () =>
    flow.value &&
    !pending.value &&
    !busy.value &&
    props.online &&
    props.state.outbound_enabled &&
    phone.value.trim() &&
    units.value > 0 &&
    estimated.value <= props.state.send_max_segments,
)
const canConfirm = computed(
  () =>
    current.value?.state === 'prepared' &&
    !attempted.value &&
    !busy.value &&
    props.online &&
    props.state.outbound_enabled &&
    consent.value &&
    retyped.value === current.value.phone,
)
function date(ts) {
  return ts ? new Date(ts * 1000).toLocaleString('zh-CN', { hour12: false }) : '尚未下发'
}
function phase(o) {
  return phaseLabels[o.state] || '结果未知'
}
function delivery(o) {
  return deliveryLabels[o.delivery] || '送达报告未收到'
}
async function perform(action) {
  if (busy.value || !flow.value) return
  busy.value = true
  error.value = ''
  try {
    await action()
  } catch (e) {
    error.value = e.message
  } finally {
    sync()
    busy.value = false
  }
  await loadHistory().catch(() => {})
}
async function prepare() {
  resetConsent()
  await perform(() =>
    flow.value.prepare({ slot: Number(slot.value), phone: phone.value, body: body.value }),
  )
}
async function confirm() {
  if (canConfirm.value) await perform(() => flow.value.confirm(retyped.value, consent.value))
}
async function cancel() {
  resetConsent()
  await perform(() => flow.value.cancel())
}
async function select(o) {
  resetConsent()
  await perform(() => flow.value.select(o.id))
}
async function recover() {
  await perform(() => flow.value.recover())
}
function release() {
  try {
    flow.value.release()
    sync()
    phone.value = ''
    body.value = ''
    resetConsent()
    error.value = ''
  } catch (e) {
    error.value = e.message
  }
}
async function loadHistory(more = false) {
  await history.load(cardFilter.value, more)
}
async function filterHistory() {
  error.value = ''
  try {
    await loadHistory()
  } catch (e) {
    error.value = e.message
  }
}
async function more() {
  busy.value = true
  try {
    await loadHistory(true)
  } catch (e) {
    error.value = e.message
  } finally {
    busy.value = false
  }
}
async function refresh() {
  if (stopped || busy.value || polling) return
  polling = true
  try {
    await loadHistory()
    if (!stopped && flow.value?.id) await flow.value.recover()
    sync()
  } catch (e) {
    error.value = e.message
    sync()
  } finally {
    polling = false
  }
}
onMounted(async () => {
  try {
    flow.value = createOutboxFlow({
      request: props.request,
      storage: localStorage,
      key: 'sim-outgoing:' + props.state.device_id,
    })
    sync()
    timer = setInterval(refresh, 5000)
    await refresh() // GET only; no automatic prepare or confirm.
  } catch {
    error.value = '浏览器无法保存操作标识，已禁用发送。请允许此站点本地存储。'
  }
})
onUnmounted(() => {
  stopped = true
  clearInterval(timer)
  history.dispose()
})
</script>
<template>
  <div class="outbox">
    <div v-if="error" class="notice error" role="alert">{{ error }}</div>
    <section class="panel compose">
      <div class="detail-head">
        <div>
          <p class="eyebrow">OUTGOING</p>
          <h2>发送短信</h2>
        </div>
        <span class="badge">{{ state.outbound_enabled ? '需要确认' : '外发关闭' }}</span>
      </div>
      <p class="tiny muted">
        每卡至少间隔 {{ state.send_interval_seconds }} 秒，每日最多
        {{ state.send_daily_limit }} 次尝试。结果未知也计入限制；次数和估算段数不是账单。
      </p>
      <p v-if="!state.outbound_enabled" class="notice">
        <template v-if="state.outbound_restore_blocked"
          >恢复后的数据库已阻止外发。请到“数据维护”核对完整发送历史、保存备份并完成迁移确认；不能仅修改配置重新开启。</template
        >
        <template v-else>外发默认关闭。确认实物兼容与资费后，再在服务配置中开启。</template>
      </p>
      <form v-if="!pending" class="compose-form" @submit.prevent="prepare">
        <div class="compose-fields">
          <label
            >发送卡槽<select v-model="slot" :disabled="busy">
              <option v-for="option in slotOptions" :key="option.value" :value="option.value">
                {{ option.label }}
              </option>
            </select></label
          >
          <label
            >目标号码<input
              v-model="phone"
              type="tel"
              inputmode="tel"
              autocomplete="off"
              maxlength="21"
              required
              :disabled="busy"
              placeholder="请核对国家码与号码"
          /></label>
        </div>
        <label
          >短信正文<textarea
            v-model="body"
            rows="4"
            maxlength="4000"
            required
            :disabled="busy"
            placeholder="写下要发送的内容"
          />
        </label>
        <div class="compose-foot">
          <span class="tiny muted"
            >保守估算 {{ estimated }} 段 · 当前最多 {{ state.send_max_segments }} 段</span
          ><button class="primary" :disabled="!canPreview">
            {{ busy ? '准备中…' : '预览（不发送）' }}
          </button>
        </div>
      </form>
      <div v-else-if="current" class="send-preview">
        <div class="preview-target">
          <span class="sim-icon">SIM {{ current.slot }}</span>
          <div>
            <h3>{{ current.phone }}</h3>
            <p class="tiny">关联卡 · {{ outgoingCardLabel(current) }}</p>
            <p class="tiny muted">
              操作时卡槽 {{ current.slot }} · 保守估算 {{ current.segments_estimate }} 段
              <span v-if="current.card_tail"> · SIM编号尾号 {{ current.card_tail }}</span>
            </p>
          </div>
        </div>
        <p class="body-text">{{ current.body }}</p>
        <div class="result-line">
          <strong>{{ phase(current) }}</strong
          ><span :class="{ 'delivery-good': current.delivery === 'delivered' }">{{
            delivery(current)
          }}</span>
        </div>
        <p v-if="current.dispatched_at" class="tiny muted">
          最近确认阶段：{{ phaseLabels[current.last_phase] }} · 下发于
          {{ date(current.dispatched_at)
          }}<span v-if="current.delivery_expected">
            · 已收到送达报告 {{ current.delivery_received }}/{{
              current.delivery_expected
            }}
            段</span
          >
        </p>
        <p v-if="current.state === 'submitted'" class="tiny muted">
          发送环节已成功；送达报告由运营商另行返回，可能不提供。送达不代表已读，未收到报告也不会自动重发。
        </p>
        <p
          v-if="current.state === 'unknown' || (attempted && current.state === 'prepared')"
          class="notice error"
        >
          本次可能已产生费用。不会自动重发；刷新只查询原记录。请先核对结果，勿另建相同内容重试。
        </p>
        <form
          v-if="current.state === 'prepared' && !attempted"
          class="confirm-form"
          @submit.prevent="confirm"
        >
          <p class="tiny muted">
            预览有效至 {{ date(current.expires_at) }}。确认前会再次查询这张 SIM 卡。
          </p>
          <label
            >再次输入目标号码<input
              v-model="retyped"
              type="tel"
              inputmode="tel"
              autocomplete="off"
              :disabled="busy"
          /></label>
          <label class="consent"
            ><input
              v-model="consent"
              type="checkbox"
              :disabled="busy"
            />我已核对卡槽、号码和正文，接受运营商短信费用。</label
          >
          <button class="primary" :disabled="!canConfirm">
            {{ busy ? '正在确认…' : '确认发送一次' }}
          </button>
        </form>
        <p v-if="attempted && current.state === 'prepared'" class="tiny muted">
          仅未下发的预览可以取消；取消成功后才重新准备。
        </p>
        <div class="outgoing-actions">
          <button @click="recover" :disabled="busy">只查询这次结果</button>
          <button v-if="current.state === 'prepared'" @click="cancel" :disabled="busy">
            取消未下发预览
          </button>
          <button v-else @click="release" :disabled="busy">新建另一条短信</button>
        </div>
      </div>
      <div v-else class="notice error">
        <p>正在恢复原操作。找不到记录或连接失败时，不会重新发送。</p>
        <button @click="recover" :disabled="busy">查询原操作</button>
      </div>
    </section>
    <section class="inbox panel">
      <div class="inbox-heading">
        <div>
          <p class="eyebrow">HISTORY</p>
          <h2>
            发件记录 <span class="count">{{ rows.length }}</span>
            <InfoTip label="发件箱说明"
              >按卡身份查看历史，换槽后仍归同一张卡。关联名称和号码来自当前卡记录；操作时卡槽单独保留。</InfoTip
            >
          </h2>
        </div>
        <button @click="refresh" :disabled="busy">刷新记录</button>
      </div>
      <form class="filters" @submit.prevent="filterHistory">
        <label class="card-filter"
          >发件卡<select v-model="cardFilter" @change="filterHistory" :disabled="busy">
            <option v-for="option in filterOptions" :key="option.value" :value="option.value">
              {{ option.label }}
            </option>
          </select></label
        >
      </form>

      <div v-if="!rows.length" class="empty">
        <span class="empty-icon">↗</span>
        <h3>{{ cardFilter ? '这张卡没有匹配的发件记录' : '还没有外发操作' }}</h3>
        <p class="muted">预览、发送尝试和结果都会留在这里。</p>
      </div>
      <ul v-else class="messages">
        <li v-for="o in rows" :key="o.id">
          <button class="message-row" @click="select(o)" :disabled="busy">
            <span class="mini-sim" :aria-label="'操作时卡槽 ' + o.slot">{{ o.slot }}</span>
            <div class="message-main">
              <div class="message-title">
                <strong>{{ o.phone }}</strong
                ><span class="badge">{{ phase(o) }}</span>
              </div>
              <p>{{ o.body }}</p>
              <span class="tiny outgoing-source">关联卡 · {{ outgoingCardLabel(o) }}</span>
              <span class="tiny muted outgoing-source"
                >操作时卡槽 {{ o.slot }} · {{ delivery(o) }}</span
              >
            </div>
            <time>{{ date(o.created_at) }}</time>
          </button>
        </li>
      </ul>
      <div v-if="next" class="load-more">
        <button @click="more" :disabled="busy">更早的记录</button>
      </div>
    </section>
  </div>
</template>
