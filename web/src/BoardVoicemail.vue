<script setup>
import { ref, watch, computed, onUnmounted } from 'vue'
import { uploadAddressView } from './recording-settings.js'
import InfoTip from './InfoTip.vue'
import {
  createBoardSettingsFlow,
  voicemailApplyNote,
  voicemailPlan,
  voicemailSlotEvidence,
} from './board-settings-flow.js'
const props = defineProps({ request: Function, state: Object, appSettings: Object })
const emit = defineEmits(['busy'])
const voicemail = ref(null),
  busy = ref(false),
  error = ref(''),
  note = ref(''),
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
const neededSMSSlots = computed(() =>
  preview.value?.sendsSMS
    ? preview.value.slots.filter((number) => {
        const slot = voicemail.value.slots.find((item) => item.slot === number)
        return voicemailConsent.value === 'enable'
          ? slot?.mode !== 2
          : slot?.mode !== 0 || slot?.tts !== ''
      })
    : [],
)
const smsReady = computed(
  () =>
    neededSMSSlots.value.length === 0 ||
    (neededSMSSlots.value.every(
      (slot) => (slot === 1 ? smsPhone1.value : smsPhone2.value).trim() !== '',
    ) &&
      smsPassword.value !== '' &&
      smsCost.value),
)
const flow = createBoardSettingsFlow({ request: (...args) => props.request(...args) })
watch(busy, (value) => emit('busy', value))
watch(
  () => [props.appSettings?.revision, props.appSettings?.ready],
  () => {
    voicemail.value = null
    runningVoicemail.value = null
    voicemailConsent.value = ''
    smsPassword.value = ''
    smsCost.value = false
    stopAuto.value = false
  },
)
async function query(action) {
  busy.value = true
  error.value = ''
  note.value = ''
  voicemail.value = null
  runningVoicemail.value = null
  try {
    const r = await flow.query(action)
    if (!r) return
    voicemail.value = r.voicemail
    runningVoicemail.value = r.running || null
    voicemailConsent.value = ''
    smsPassword.value = ''
    smsCost.value = false
    stopAuto.value = false
    uncertain.value = false
  } catch (e) {
    error.value = e.message
  } finally {
    busy.value = false
  }
}
async function applyVoicemail(intent) {
  if (
    voicemailConsent.value !== intent ||
    !preview.value?.ready ||
    (intent === 'enable' && !appReady.value)
  )
    return
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
      if (runningVoicemail.value && appReady.value)
        runningVoicemail.value.url_matches = r.voicemail.amr_url === props.appSettings.url
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
onUnmounted(() => flow.dispose())
const appReady = computed(() => props.appSettings?.ready === true)
const targetAddress = computed(() => uploadAddressView(props.appSettings?.url || ''))
const addressStatus = computed(() => {
  if (!appReady.value)
    return '上方设置尚未保存或生效。启用前请先处理，再重新读取板子；停用以当前运行配置和下方预览为准。'
  if (!voicemail.value?.amr_url) return '板子未设置上传地址。'
  return voicemail.value.amr_url === props.appSettings?.url
    ? '与应用当前接收地址一致。'
    : '与应用当前接收地址不同。启用前请核对；停用会清空板子上传地址。'
})
</script>
<template>
  <section class="panel compose recording-board">
    <h2>
      2. 板子接听与上传
      <InfoTip label="留言设置说明"
        >上传地址直接取自上方保存并生效的应用设置，这里不再填写第二遍。读取只走
        TCP；部分固件修改接听方式和清除预设提示语需要设置短信。应用的提示文字在接通后下发，不保存为板子预设。</InfoTip
      >
    </h2>
    <p
      v-if="state && state.voicemail_enabled !== true && state.environment !== 'cloud'"
      class="notice"
    >
      应用接收服务当前未开启。请先完成上方设置并重启应用；停止板子自动接听不受影响。
    </p>
    <p class="muted tiny">先读取，再选择启用或停用并核对修改预览。上传地址沿用第一步，无需再填。</p>
    <p v-if="error" class="notice error" role="alert">{{ error }}</p>
    <p v-if="note" class="notice voicemail-preview" role="status">{{ note }}</p>
    <button type="button" :disabled="busy || !state?.online" @click="query('voicemail_query')">
      读取板子设置（不发短信）
    </button>
    <template v-if="voicemail">
      <p v-for="slot in voicemail.slots" :key="slot.slot">{{ slotLine(slot) }}</p>
      <dl class="recording-addresses">
        <div>
          <dt>板子当前上传地址 · TCP 已读回</dt>
          <dd>{{ uploadAddressView(voicemail.amr_url) }}</dd>
        </div>
        <div>
          <dt>应用当前接收地址 · 只读</dt>
          <dd>{{ appReady ? targetAddress : '尚未确认生效' }}</dd>
        </div>
      </dl>
      <p class="tiny muted">
        {{ addressStatus }} 地址中的令牌和查询参数已隐藏，是否一致按完整地址核对。
      </p>
      <p class="tiny muted">两张卡共用一个上传地址；接听方式和板子预设提示语按目标卡槽修改。</p>
      <label class="consent"
        ><input
          v-model="voicemailConsent"
          type="radio"
          value="enable"
          :disabled="busy || uncertain || !appReady || state?.environment === 'cloud'"
        />启用：设置自动接听录音，并同步应用的上传地址</label
      >
      <label class="consent"
        ><input
          v-model="voicemailConsent"
          type="radio"
          value="restore"
          :disabled="busy || uncertain"
        />停用：停止自动接听，清除板子预设提示语和上传地址</label
      >
      <p v-if="preview" class="notice voicemail-preview">{{ preview.text }}</p>
      <p v-if="preview?.sendsSMS" class="tiny">
        本次最多发送 {{ preview.smsCount }} 条设置短信，可能产生费用，并计入对应卡的发送额度。
      </p>
      <p v-else-if="preview" class="tiny">本次操作无需发送设置短信。</p>
      <p v-if="voicemail.channel === 'sms'" class="tiny muted">
        上传地址已通过 TCP 读取，不需要短信。当前固件不能通过 TCP
        读回接听方式和板子预设提示语；修改它们时，会由对应 SIM
        向自己的号码发送设置短信。未确认不代表已关闭，也不代表设置已生效。
      </p>
      <p v-else-if="voicemailConsent === 'enable'" class="tiny muted">
        仅修改预览列出的卡槽。板子会自动接听并录音，提示语由应用在接通后播放。
      </p>
      <template v-if="voicemail.channel === 'sms' && preview?.sendsSMS">
        <label v-if="neededSMSSlots.includes(1)"
          >卡槽 1 自己的号码（由卡 1 发给自己）<input
            v-model="smsPhone1"
            autocomplete="off"
            inputmode="tel"
        /></label>
        <label v-if="neededSMSSlots.includes(2)"
          >卡槽 2 自己的号码（由卡 2 发给自己）<input
            v-model="smsPhone2"
            autocomplete="off"
            inputmode="tel"
        /></label>
        <label
          >板子短信指令用户密码<input
            v-model="smsPassword"
            type="password"
            autocomplete="new-password"
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
      <button
        type="button"
        class="primary"
        :disabled="
          busy ||
          uncertain ||
          !voicemailConsent ||
          !preview?.ready ||
          (voicemailConsent === 'enable' && !appReady) ||
          !smsReady ||
          !state?.online ||
          (voicemail.channel === 'sms' && voicemailConsent === 'restore' && !stopAuto)
        "
        @click="applyVoicemail(voicemailConsent)"
      >
        确认执行一次
      </button>
      <details class="settings-group settings-advanced">
        <summary>也可以在板子后台手动配置</summary>
        <p class="tiny muted">
          能访问板子的 /mgr
          时，可在那里设置来电应答，并配置同一个上传地址，无需再发送重复设置短信。应用不会自动把人工配置标记为已确认；TCP
          无法读回的项目仍需在板子后台核对。
        </p>
      </details>
    </template>
  </section>
</template>
