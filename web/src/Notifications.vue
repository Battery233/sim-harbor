<script setup>
import InfoTip from './InfoTip.vue'
import {
  createNotificationRecords,
  notificationKinds,
  notificationRecordLink,
  notificationCanceledReason,
} from './notification-records.js'
import { ref, computed, onMounted, onUnmounted } from 'vue'
import {
  createNotificationActions,
  canRetryNotification,
  notificationErrors,
} from './notification-flow.js'
const props = defineProps({ request: Function })
const data = ref(null),
  rows = ref([]),
  filter = ref('all'),
  source = ref('all'),
  next = ref(''),
  busy = ref(false),
  error = ref(''),
  note = ref('')
const testConsent = ref(false),
  retryConsent = ref(false),
  retryRow = ref(null),
  now = ref(Date.now() / 1000)
const actions = createNotificationActions({ request: (...args) => props.request(...args) })
let stopped = false,
  clock
const canTest = computed(
  () =>
    data.value?.enabled &&
    !busy.value &&
    testConsent.value &&
    (!data.value.summary.test_at || now.value - data.value.summary.test_at >= 60),
)
function date(ts) {
  return ts ? new Date(ts * 1000).toLocaleString('zh-CN', { hour12: false }) : '尚未测试'
}
function reason(code) {
  return notificationErrors[code] || '等待尝试'
}
const records = createNotificationRecords({
  request: (...args) => props.request(...args),
  changed(view) {
    data.value = view
    rows.value = view.items
    next.value = view.next_before
  },
})
async function load(more = false) {
  await records.load({ state: filter.value, source: source.value }, more)
}
async function refresh(more = false) {
  if (busy.value || stopped) return
  busy.value = true
  error.value = ''
  try {
    await load(more)
  } catch (e) {
    error.value = e.message
  } finally {
    busy.value = false
  }
}
async function perform(action, success) {
  if (busy.value || stopped) return
  busy.value = true
  error.value = ''
  note.value = ''
  try {
    await action()
    note.value = success
  } catch (e) {
    error.value = e.message
  } finally {
    testConsent.value = false
    retryConsent.value = false
    retryRow.value = null
    await load().catch(() => {}) // Read-only recovery; no automatic test or retry.
    busy.value = false
  }
}
function selectRetry(row) {
  retryRow.value = row
  retryConsent.value = false
  note.value = ''
}
function testPush() {
  if (canTest.value)
    return perform(
      () => actions.test(testConsent.value),
      'Bark 已接受测试请求，请在 iPhone 上核对通知。',
    )
}
function retryPush() {
  if (retryRow.value && retryConsent.value)
    return perform(
      () => actions.retry(retryRow.value.id, true),
      '已提前安排原任务；后台会尝试推送，不重发短信。',
    )
}
onMounted(async () => {
  clock = setInterval(() => (now.value = Date.now() / 1000), 1000)
  await refresh()
})
onUnmounted(() => {
  stopped = true
  clearInterval(clock)
  records.dispose()
})
</script>
<template>
  <div class="outbox">
    <p v-if="error" class="notice error" role="alert">{{ error }}</p>
    <p v-if="note" class="notice" role="status">{{ note }}</p>
    <section class="panel compose">
      <div class="detail-head">
        <div>
          <p class="eyebrow">NOTIFICATIONS</p>
          <h2>
            短信与通话推送
            <InfoTip label="短信与通话推送说明"
              >通知失败不会丢失短信。Bark 接受请求后，仍需在 iPhone 核对；重复尝试可能重复通知。<br />凭据可在系统配置中修改，保存后另行确认重启应用生效；旧密钥不回显。启用不会补推过去的短信。</InfoTip
            >
          </h2>
        </div>
        <span class="badge">{{ data?.enabled ? '已配置' : '未启用' }}</span>
      </div>

      <p class="muted tiny">
        这里仅展示短信与通话的 Bark
        推送记录，包括未接来电、留言、录音未到和迟到录音。不含运行异常与容量提醒。
      </p>
      <dl class="notification-config">
        <div>
          <dt>推送服务</dt>
          <dd>{{ data?.bark_host || '尚未读取' }}</dd>
        </div>
        <div>
          <dt>通知打开的管理地址</dt>
          <dd>{{ data?.public_url || '尚未设置' }}</dd>
        </div>
      </dl>

      <div class="notification-summary">
        <span>待推送 {{ data?.summary?.pending || 0 }}</span
        ><span>失败待重试 {{ data?.summary?.retrying || 0 }}</span
        ><span>推送成功 {{ data?.summary?.accepted || 0 }}</span>
      </div>
      <div class="notification-test">
        <p class="tiny">
          最近连接测试 · {{ date(data?.summary?.test_at) }}<br />{{
            data?.summary?.test_at ? reason(data.summary.test_outcome) : '尚未发出测试'
          }}
        </p>
        <label class="consent"
          ><input
            v-model="testConsent"
            type="checkbox"
            :disabled="!data?.enabled || busy"
          />向当前配置的 iPhone 发一条连接测试</label
        ><button class="primary" :disabled="!canTest" @click="testPush">发送连接测试</button>
        <p class="tiny muted">每分钟最多一次，结果未知只查询；连接测试不经过 SIM 卡。</p>
      </div>
    </section>
    <section class="panel compose">
      <div class="detail-head">
        <h2>推送记录</h2>
        <button :disabled="busy" @click="refresh(false)">刷新记录</button>
      </div>
      <div class="notification-filters">
        <label class="notification-filter"
          >类型<select v-model="source" :disabled="busy" @change="refresh(false)">
            <option value="all">短信与通话</option>
            <option value="sms">仅短信</option>
            <option value="call">仅通话</option>
          </select></label
        >
        <label class="notification-filter"
          >查看<select v-model="filter" :disabled="busy" @change="refresh(false)">
            <option value="all">全部记录</option>
            <option value="pending">待推送</option>
            <option value="sent">推送成功</option>
            <option value="restore_held">恢复后暂停</option>
            <option value="canceled">已取消</option>
          </select></label
        >
      </div>
      <p v-if="!rows.length" class="empty muted">{{ busy ? '正在读取…' : '当前没有通知任务' }}</p>
      <ul class="notification-list">
        <li v-for="row in rows" :key="row.key">
          <div class="result-line">
            <strong>{{ notificationKinds[row.kind] }} · 卡槽 {{ row.slot }}</strong
            ><span class="badge">{{
              row.state === 'canceled'
                ? '已取消'
                : row.state === 'restore_held'
                  ? '恢复后暂停'
                  : row.state === 'sent'
                    ? '推送成功'
                    : row.failures
                      ? '失败待重试'
                      : '等待推送'
            }}</span>
          </div>
          <p v-if="row.state !== 'sent' || row.failures" class="tiny muted">
            <template v-if="row.failures">此前失败 {{ row.failures }} 次</template>
            <template v-if="row.failures && row.state !== 'sent'"> · </template>
            {{
              row.state === 'canceled'
                ? notificationCanceledReason(row)
                : row.state === 'restore_held'
                  ? '恢复后的旧任务暂停，不自动补推'
                  : row.state === 'sent'
                    ? ''
                    : reason(row.last_error)
            }}<br /><template v-if="row.state === 'pending'">{{
              row.next_at > now ? '下次尝试 ' + date(row.next_at) : '已进入可尝试队列'
            }}</template>
          </p>
          <p class="tiny muted">
            记录时间 {{ date(row.recorded_at)
            }}<span v-if="row.accepted_at"> · Bark 接受于 {{ date(row.accepted_at) }}</span>
          </p>
          <div class="outgoing-actions">
            <span v-if="row.message_deleted" class="tiny muted">原短信已删除</span>
            <a :href="notificationRecordLink(row)">{{
              row.source === 'call' ? '查看来电记录' : '查看原短信'
            }}</a
            ><button
              v-if="data?.enabled && canRetryNotification(row, now)"
              :disabled="busy"
              @click="selectRetry(row)"
            >
              提前重试此通知
            </button>
          </div>
        </li>
      </ul>
      <div v-if="retryRow" class="notice">
        <p>提前重试通知 #{{ retryRow.id }}：iPhone 可能再次收到同一条通知，原短信不会重新发送。</p>
        <label class="consent"
          ><input
            v-model="retryConsent"
            type="checkbox"
            :disabled="busy"
          />确认提前重试这条任务</label
        >
        <div class="outgoing-actions">
          <button class="primary" :disabled="busy || !retryConsent" @click="retryPush">
            确认重试通知</button
          ><button :disabled="busy" @click="retryRow = null">取消</button>
        </div>
      </div>
      <button v-if="next" :disabled="busy" @click="refresh(true)">查看更早任务</button>
    </section>
  </div>
</template>
