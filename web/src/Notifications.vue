<script setup>
import InfoTip from './InfoTip.vue'
import { ref, computed, onMounted, onUnmounted } from 'vue'
import {
  createNotificationActions,
  canRetryNotification,
  notificationErrors,
} from './notification-flow.js'
const props = defineProps({ request: Function })
const data = ref(null),
  rows = ref([]),
  filter = ref('pending'),
  next = ref(0),
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
async function load(more = false) {
  const query = new URLSearchParams({ state: filter.value })
  if (more) query.set('before', next.value)
  const result = await props.request('/notifications?' + query)
  if (stopped) return
  if (!Array.isArray(result.items) || !result.summary)
    throw new Error('无法读取通知记录，请稍后刷新。')
  data.value = result
  rows.value = more ? [...rows.value, ...result.items] : result.items
  next.value = result.next_before
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
            通知诊断
            <InfoTip label="通知诊断说明"
              >通知失败不会丢失短信。Bark 接受请求后，仍需在 iPhone 核对；重复尝试可能重复通知。<br />凭据可在系统配置中修改，保存后另行确认重启应用生效；旧密钥不回显。启用不会补推过去的短信。</InfoTip
            >
          </h2>
        </div>
        <span class="badge">{{ data?.enabled ? '已配置' : '未启用' }}</span>
      </div>

      <p class="muted tiny">这里的任务和汇总为短信通知；未接来电提醒状态请查看“来电记录”。</p>
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
        <span>待推送 {{ data?.summary.pending || 0 }}</span
        ><span>失败待重试 {{ data?.summary.retrying || 0 }}</span
        ><span>Bark 已接受 {{ data?.summary.accepted || 0 }}</span>
      </div>
      <div class="notification-test">
        <p class="tiny">
          最近连接测试 · {{ date(data?.summary.test_at) }}<br />{{
            data?.summary.test_at ? reason(data.summary.test_outcome) : '尚未发出测试'
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
        <h2>通知任务</h2>
        <button :disabled="busy" @click="refresh(false)">刷新记录</button>
      </div>
      <label class="notification-filter"
        >查看<select v-model="filter" :disabled="busy" @change="refresh(false)">
          <option value="pending">待推送</option>
          <option value="sent">Bark 已接受</option>
          <option value="restore_held">恢复后暂停</option>
          <option value="canceled">短信删除后取消</option>
          <option value="all">全部记录</option>
        </select></label
      >
      <p v-if="!rows.length" class="empty muted">{{ busy ? '正在读取…' : '当前没有通知任务' }}</p>
      <ul class="notification-list">
        <li v-for="row in rows" :key="row.id">
          <div class="result-line">
            <strong>卡槽 {{ row.slot }} · 通知 #{{ row.id }}</strong
            ><span class="badge">{{
              row.state === 'canceled'
                ? '已取消'
                : row.state === 'restore_held'
                  ? '恢复后暂停'
                  : row.state === 'sent'
                    ? 'Bark 已接受'
                    : row.failures
                      ? '失败待重试'
                      : '等待推送'
            }}</span>
          </div>
          <p class="tiny muted">
            失败 {{ row.failures }} 次 ·
            {{
              row.state === 'canceled'
                ? '原短信已删除，不再推送'
                : row.state === 'restore_held'
                  ? '旧任务不自动重推；原短信仍保留'
                  : row.state === 'sent'
                    ? '手机收到情况待核对'
                    : reason(row.last_error)
            }}<br /><template v-if="row.state === 'pending'">{{
              row.next_at > now ? '下次尝试 ' + date(row.next_at) : '已进入可尝试队列'
            }}</template>
          </p>
          <div class="outgoing-actions">
            <span v-if="row.message_deleted" class="tiny muted">原短信已删除</span>
            <a :href="'?message=' + row.message_id">查看原短信</a
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
