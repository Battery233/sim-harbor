<script setup>
import { ref, watch, computed, onUnmounted } from 'vue'
import BoardHistory from './BoardHistory.vue'
import { boardStatusView } from './board-history-flow.js'
import InfoTip from './InfoTip.vue'
import { createBoardSettingsFlow } from './board-settings-flow.js'
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
  uncertain = ref(false)
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
  if (voicemailConsent.value !== intent) return
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
      if (r.verified === true) {
        note.value = r.unchanged
          ? '板子留言参数已与目标一致，未下发修改。'
          : intent === 'enable'
            ? '留言参数已下发并读回。'
            : '已恢复为不接听，并读回确认。'
      } else {
        note.value = r.url_verified
          ? '短信已提交。来电方式尚未经控制短信确认，不会自动重发。上传地址已用 TCP 读回。'
          : '短信已提交。上传地址和来电方式都还没有确认，不会自动重发。'
      }
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
  const names = { 0: '不接听', 1: '接听', 2: '接听并录音', 3: '挂断' }
  if (slot.mode == null && typeof slot.tts !== 'string')
    return `卡槽 ${slot.slot}：TCP 未返回来电方式`
  const mode = slot.mode == null ? '来电方式未返回' : names[slot.mode] || '待核对'
  const preset =
    typeof slot.tts !== 'string'
      ? '提示音预设未读取'
      : slot.tts
        ? '板子上仍有提示音预设'
        : '提示音预设为空'
  return `卡槽 ${slot.slot}：${mode} · ${preset}`
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
        >这里通过板子的正式TCP连接读取或修改参数，与管理应用的系统配置不同。不会自动修改WiFi、启用SIM流量或重启板子。写入结果未知先查询，不重复下发。</InfoTip
      >
    </h2>
    <p v-if="error" class="notice error" role="alert">{{ error }}</p>
    <p v-if="note" class="notice" role="status">{{ note }}</p>
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
        留言
        <InfoTip label="留言设置说明"
          >上传地址用 TCP askamrurl / setamrurl。来电方式先试
          TCP；固件拒绝时，确认后才从该卡槽向它自己的号码发送模式短信。启用不再下发提示音预设，接通后由
          Harbor
          发送一次。读取不会发短信。启用前先在系统配置打开留言并重启。录音只在本地部署上传；云端管理台不接收这段录音。恢复会把已配置卡槽改回不接听，并清空仍留在板子上的提示音预设和上传地址。没有控制短信确认时，页面不会把目标来电方式写成已读回，也不会自动重发。</InfoTip
        >
      </h3>
      <button type="button" :disabled="busy || !state?.online" @click="query('voicemail_query')">
        读取留言参数
      </button>
      <template v-if="voicemail">
        <p v-for="slot in voicemail.slots" :key="slot.slot">{{ slotLine(slot) }}</p>
        <p class="tiny muted">上传地址：{{ voicemail.amr_url || '空' }}</p>
        <p v-if="voicemail.channel === 'sms'" class="tiny muted">
          固件拒绝了 TCP
          来电方式。读取不会发短信。启用会先核对这张卡当前的本机号码、外发开关和恢复保护，再发送模式短信，可能产生费用。停止自动接听要单独确认；外发被关掉时仍可发送这条停止短信，但不会悄悄当作普通外发。没有控制短信确认时只记为已提交，来电方式保持未知，不会自动重发。若板子上仍有旧的提示音预设，恢复会再发一条清空短信。
        </p>
        <p v-else class="tiny muted">
          启用将按已生效的系统配置写入接听并录音和局域网上传地址，不改已有提示音预设。其他卡槽保持刚才读到的值。接通后的提示音由
          Harbor 发送一次。
        </p>
        <template v-if="voicemail.channel === 'sms'">
          <label
            >卡槽 1 本机号码<input v-model="smsPhone1" autocomplete="off" inputmode="tel"
          /></label>
          <label
            >卡槽 2 本机号码<input v-model="smsPhone2" autocomplete="off" inputmode="tel"
          /></label>
          <label
            >用户密码<input v-model="smsPassword" type="password" autocomplete="new-password"
          /></label>
          <label class="consent"
            ><input
              v-model="smsCost"
              type="checkbox"
              :disabled="busy || uncertain"
            />我知道控制短信会占用该卡短信并可能产生费用</label
          >
          <label v-if="voicemailConsent === 'restore'" class="consent"
            ><input
              v-model="stopAuto"
              type="checkbox"
              :disabled="busy || uncertain"
            />我确认停止自动接听。外发关闭或恢复保护未解除时，仍只发送这条停止短信</label
          >
        </template>
        <label class="consent"
          ><input
            v-model="voicemailConsent"
            type="radio"
            value="enable"
            :disabled="busy || uncertain"
          />确认启用留言</label
        >
        <label class="consent"
          ><input
            v-model="voicemailConsent"
            type="radio"
            value="restore"
            :disabled="busy || uncertain"
          />确认恢复为不接听并清除提示音和上传地址</label
        >
        <button
          type="button"
          class="primary"
          :disabled="
            busy ||
            uncertain ||
            !voicemailConsent ||
            !smsReady ||
            !state?.online ||
            (voicemail.channel === 'sms' && voicemailConsent === 'restore' && !stopAuto)
          "
          @click="applyVoicemail(voicemailConsent)"
        >
          确认下发一次
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
