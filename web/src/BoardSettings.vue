<script setup>
import { ref, watch, computed, onUnmounted } from 'vue'
import BoardHistory from './BoardHistory.vue'
import { boardStatusView } from './board-history-flow.js'
import InfoTip from './InfoTip.vue'
import {
  createBoardSettingsFlow,
  voicemailApplyNote,
  voicemailPlan,
  voicemailSetupSteps,
  voicemailSlotEvidence,
} from './board-settings-flow.js'
const props = defineProps({ request: Function, state: Object })
const seconds = ref(null),
  target = ref(110),
  storage = ref(null),
  voicemail = ref(null),
  busy = ref(false),
  error = ref(''),
  note = ref(''),
  consent = ref(false),
  voicemailConsent = ref(''),
  smsPhone1 = ref(''),
  smsPhone2 = ref(''),
  smsPassword = ref(''),
  smsCost = ref(false),
  stopAuto = ref(false),
  uncertain = ref(false),
  runningVoicemail = ref(null)
const preview = computed(() =>
  voicemail.value && voicemailConsent.value
    ? voicemailPlan(voicemailConsent.value, voicemail.value, runningVoicemail.value)
    : null,
)
const smsReady = computed(
  () =>
    voicemail.value?.channel !== 'sms' ||
    ((smsPhone1.value.trim() !== '' || smsPhone2.value.trim() !== '') &&
      smsPassword.value !== '' &&
      smsCost.value),
)
const showIdentity = ref(false),
  historyOpen = ref(false)
const rawStatus = computed(() =>
  JSON.stringify(boardStatusView(props.state?.reported || {}, showIdentity.value), null, 2),
)
const flow = createBoardSettingsFlow({ request: (...args) => props.request(...args) })
watch(target, () => (consent.value = false))
async function query(action) {
  busy.value = true
  error.value = ''
  consent.value = false
  try {
    const r = await flow.query(action)
    if (!r) return
    if (action === 'heartbeat_query') {
      seconds.value = r.seconds
      target.value = r.seconds
      uncertain.value = false
    } else if (action === 'voicemail_query') {
      voicemail.value = r.voicemail
      runningVoicemail.value = r.running || null
      voicemailConsent.value = ''
      smsPassword.value = ''
      smsCost.value = false
      stopAuto.value = false
      uncertain.value = false
    } else storage.value = r.storage
  } catch (e) {
    error.value = e.message
  } finally {
    busy.value = false
  }
}
async function applyVoicemail(intent) {
  if (voicemailConsent.value !== intent || !preview.value?.ready) return
  busy.value = true
  error.value = ''
  note.value = ''
  try {
    const phones = {}
    if (smsPhone1.value.trim()) phones[1] = smsPhone1.value.trim()
    if (smsPhone2.value.trim()) phones[2] = smsPhone2.value.trim()
    const sms =
      voicemail.value?.channel === 'sms'
        ? {
            phones,
            password: smsPassword.value,
            costConfirmed: smsCost.value,
            stopAutoAnswer: intent === 'restore' && stopAuto.value,
          }
        : undefined
    const r = await flow.applyVoicemail(intent, voicemail.value, true, sms)
    if (r) {
      voicemail.value = r.voicemail
      smsPassword.value = ''
      smsCost.value = false
      stopAuto.value = false
      note.value = voicemailApplyNote(intent, r)
    }
  } catch (e) {
    error.value = e.message + ' 请先重新查询，不要重复提交。'
    uncertain.value = true
  } finally {
    busy.value = false
    voicemailConsent.value = ''
  }
}
function slotLine(slot) {
  return voicemailSlotEvidence(slot, voicemail.value?.channel)
}
async function save() {
  busy.value = true
  error.value = ''
  note.value = ''
  try {
    const r = await flow.set(target.value, seconds.value, consent.value)
    if (r) {
      seconds.value = r.seconds
      note.value = r.unchanged ? '当前已是此值，未下发修改。' : '心跳间隔已修改并读回确认。'
    }
  } catch (e) {
    error.value = e.message + ' 请先重新查询，不要重复提交。'
    uncertain.value = true
  } finally {
    busy.value = false
    consent.value = false
  }
}
onUnmounted(() => flow.dispose())
</script>
<template>
  <section class="panel compose board-settings">
    <h2>
      板子设置
      <InfoTip label="板子设置说明"
        >这里读取或修改板子本身的设置。应用端的选项请到“系统配置”修改。操作不会自动修改WiFi、启用SIM流量或重启板子。结果不明时先重新读取，不要重复操作。</InfoTip
      >
    </h2>
    <p v-if="error" class="notice error" role="alert">{{ error }}</p>
    <p v-if="note" class="notice voicemail-preview" role="status">{{ note }}</p>
    <div class="settings-group">
      <h3>
        板子心跳间隔
        <InfoTip label="板子心跳说明"
          >控制板子发送998心跳的间隔，修改后立即生效。服务器会重新查询原值，只有与你确认的原值一致且连接未变才尝试写一次，随后读回确认。为避免断开，不能设置超过服务端连接超时；通常保留当前值即可。</InfoTip
        >
      </h3>
      <p>当前：{{ seconds === null ? '尚未查询' : seconds + ' 秒' }}</p>
      <button type="button" :disabled="busy || !state?.online" @click="query('heartbeat_query')">
        读取板子心跳间隔
      </button>
      <form v-if="seconds !== null" @submit.prevent="save">
        <label
          >新心跳间隔（秒）<input
            v-model.number="target"
            type="number"
            min="5"
            :max="Math.min(600, (state?.stale_seconds || 345) - 16)"
            :disabled="busy"
            required
        /></label>
        <p>修改预览：{{ seconds }} 秒 → {{ target }} 秒</p>
        <label class="consent"
          ><input
            v-model="consent"
            type="checkbox"
            :disabled="busy || uncertain"
          />确认修改板子心跳间隔，无需重启板子</label
        >
        <button class="primary" :disabled="busy || uncertain || !consent || !state?.online">
          确认修改一次
        </button>
      </form>
    </div>
    <div class="settings-group">
      <h3>
        板子自动接听与录音
        <InfoTip label="留言设置说明"
          >这里修改板子是否自动接听、录音，以及向哪里上传。读取设置不会发短信；修改时部分固件需要发送设置短信，会计入短信额度。提示语由应用在接通后播放一次，开启时不写入板子。仅关闭应用留言服务不会撤销板子设置。</InfoTip
        >
      </h3>
      <ol v-if="state?.environment !== 'cloud'" class="voicemail-steps">
        <li v-for="step in voicemailSetupSteps" :key="step">{{ step }}</li>
      </ol>
      <p v-if="state?.environment === 'cloud'" class="notice">
        当前是云端部署，不支持接收录音。请保持应用留言关闭；如板子已开启自动接听，可在这里停止。
      </p>
      <p
        v-if="state && state.voicemail_enabled !== true && state.environment !== 'cloud'"
        class="notice"
      >
        应用留言服务当前未开启。要接收录音，请先到“系统配置”开启、保存并重启。停止板子自动接听不受影响。
      </p>
      <button type="button" :disabled="busy || !state?.online" @click="query('voicemail_query')">
        读取板子留言设置
      </button>
      <template v-if="voicemail">
        <p v-for="slot in voicemail.slots" :key="slot.slot">{{ slotLine(slot) }}</p>
        <p class="tiny muted">板子录音上传地址：{{ voicemail.amr_url || '未设置' }}</p>
        <p v-if="preview" class="notice voicemail-preview">{{ preview.text }}</p>
        <p v-if="preview?.sendsSMS" class="tiny">
          本次最多发送 {{ preview.smsCount }} 条设置短信，可能产生费用，并计入对应卡的发送额度。
        </p>
        <p v-else-if="preview" class="tiny">本次操作无需发送设置短信。</p>
        <p v-if="voicemail.channel === 'sms'" class="tiny muted">
          这块板子无法直接读回来电方式，需要向该卡自己的号码发短信修改。短信已发出不等于设置已生效；未收到确认时会显示“待确认”，不会自动重发。
        </p>
        <p v-else-if="voicemailConsent === 'enable'" class="tiny muted">
          仅修改预览列出的卡槽。板子会自动接听并录音，提示语由应用在接通后播放。
        </p>
        <template v-if="voicemail.channel === 'sms'">
          <label
            >卡槽 1 本机号码<input v-model="smsPhone1" autocomplete="off" inputmode="tel"
          /></label>
          <label
            >卡槽 2 本机号码<input v-model="smsPhone2" autocomplete="off" inputmode="tel"
          /></label>
          <label
            >板子管理密码<input v-model="smsPassword" type="password" autocomplete="new-password"
          /></label>
          <label class="consent"
            ><input
              v-model="smsCost"
              type="checkbox"
              :disabled="busy || uncertain"
            />我同意发送设置短信，并了解可能产生费用</label
          >
          <label v-if="voicemailConsent === 'restore'" class="consent"
            ><input
              v-model="stopAuto"
              type="checkbox"
              :disabled="busy || uncertain"
            />我确认停止板子自动接听
            <InfoTip label="停止自动接听的短信说明"
              >即使普通短信外发关闭或恢复保护尚未解除，仍允许发送本次停用所需的设置短信；不会因此开放普通短信外发。</InfoTip
            ></label
          >
        </template>
        <label class="consent"
          ><input
            v-model="voicemailConsent"
            type="radio"
            value="enable"
            :disabled="busy || uncertain"
          />开启板子自动接听和录音</label
        >
        <label class="consent"
          ><input
            v-model="voicemailConsent"
            type="radio"
            value="restore"
            :disabled="busy || uncertain"
          />停止自动接听，清除板子提示语和上传地址</label
        >
        <button
          type="button"
          class="primary"
          :disabled="
            busy ||
            uncertain ||
            !voicemailConsent ||
            !preview?.ready ||
            !smsReady ||
            !state?.online ||
            (voicemail.channel === 'sms' && voicemailConsent === 'restore' && !stopAuto)
          "
          @click="applyVoicemail(voicemailConsent)"
        >
          确认执行一次
        </button>
      </template>
    </div>
    <div class="settings-group">
      <h3>
        板子本地存储
        <InfoTip label="板子存储说明"
          >查询板子配置中的短信与通话存储开关，和应用数据库不同。首批只读，不修改、不清空记录；厂家文档称修改开关须重启板子，读取配置不等于验证运行期间已生效。</InfoTip
        >
      </h3>
      <button type="button" :disabled="busy || !state?.online" @click="query('storage_query')">
        读取短信与通话存储开关
      </button>
      <p v-for="slot in storage ? [1, 2] : []" :key="slot">
        卡槽 {{ slot }}：短信 {{ storage[slot + 's'] ? '开启' : '关闭' }} · 通话
        {{ storage[slot + 't'] ? '开启' : '关闭' }}
      </p>
    </div>
    <details class="settings-group settings-advanced">
      <summary>
        最近一次原始状态
        <InfoTip label="原始状态说明"
          >显示应用最近一次stat读回的状态，不自动发命令；过期时可通过首页查询最新板子状态。身份默认隐藏，凭据字段始终隐藏；此处不保存、导出或修改参数。</InfoTip
        >
      </summary>
      <p class="tiny muted">
        上次查询：{{
          state?.status_received_at
            ? new Date(state.status_received_at * 1000).toLocaleString('zh-CN')
            : '尚未查询'
        }}
      </p>
      <label class="consent"
        ><input v-model="showIdentity" type="checkbox" />显示SIM完整身份信息</label
      >
      <pre class="board-raw-status">{{ rawStatus }}</pre>
    </details>
    <details class="settings-group settings-advanced" @toggle="historyOpen = $event.target.open">
      <summary>板子本地短信与通话历史</summary>
      <BoardHistory v-if="historyOpen" :request="request" :online="state?.online" />
    </details>
  </section>
</template>
