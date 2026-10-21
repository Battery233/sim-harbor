<script setup>
import InfoTip from './InfoTip.vue'
import { ref, computed, watch, onMounted, onUnmounted } from 'vue'
import {
  settingsSectionValues,
  normalizedSettingsValues,
  recordingMode,
  recordingEnableProblem,
} from './recording-settings.js'
import { createSettingsFlow, settingsSavedNote, voicemailServiceStatus } from './settings-flow.js'
const props = defineProps({
  request: Function,
  state: Object,
  locked: Boolean,
  section: { type: String, default: 'system' },
})
const emit = defineEmits(['configuration-status', 'changed'])
const recording = computed(() => props.section === 'recording')
const snapshot = ref(null),
  values = ref({}),
  secrets = ref({}),
  sources = ref(''),
  busy = ref(false),
  error = ref(''),
  note = ref(''),
  consent = ref(false),
  voicemailConsent = ref(false),
  restartConsent = ref(false),
  uncertain = ref(false),
  restarting = ref(false)
const flow = createSettingsFlow({ request: (...args) => props.request(...args) })
let stopped = false,
  poll
const groups = [
  {
    title: '管理访问',
    fields: [['admin_token_header', '自定义鉴权请求头（留空使用 Authorization）', 'text']],
  },
  {
    title: '管理网址与时间',
    fields: [
      ['public_url', '通知跳转的管理网址', 'text'],
      ['device_timezone', '记录解析与统计时区', 'text'],
    ],
  },
  {
    title: 'Bark与短信',
    fields: [
      ['bark_base_url', 'Bark服务器地址', 'text'],
      ['send_interval_seconds', '每卡发信最短间隔（秒）', 'number', 10, 86400],
      ['send_daily_limit', '每卡每天最多尝试', 'number', 1, 100],
      ['send_max_segments', '每条短信最多段数', 'number', 1, 10],
    ],
  },
  {
    title: '日志',
    fields: [
      ['log_max_bytes', '单个日志文件上限（字节）', 'number', 65536, 16777216],
      ['log_backups', '旧日志文件数量', 'number', 1, 5],
      ['diagnostics_limit', '网页最近诊断条数', 'number', 100, 5000],
      ['log_retention_days', '磁盘日志保留天数', 'number', 1, 30],
    ],
  },
]
const connectionFields = [
  [
    'device_id',
    '允许接入的设备编号',
    'text',
    '与板子上报的编号匹配；填错会拒绝上报，不会更改板子的编号。',
  ],
  [
    'http_address',
    '服务端网页监听地址',
    'text',
    '管理应用提供网页的地址；修改后可能需要通过新地址访问。',
  ],
  [
    'tcp_address',
    '服务端TCP监听地址',
    'text',
    '管理应用接收板子连接的地址；不会自动修改板子的连接目标。',
  ],
]
const secretFields = [
  ['aes_key', 'AES Key（16字节）'],
  ['aes_iv', 'AES IV（16字节）'],
  ['admin_token', '管理访问凭据（至少24字符）'],
  ['bark_key', 'Bark Key'],
]
const voicemailStatus = computed(() =>
  voicemailServiceStatus({
    savedEnabled: snapshot.value?.values?.voicemail_enabled === true,
    runningEnabled: props.state ? props.state.voicemail_enabled === true : null,
    pendingRestart: snapshot.value?.pending_restart === true,
  }),
)
const enablingVoicemail = computed(
  () =>
    recording.value &&
    values.value.voicemail_enabled === true &&
    (snapshot.value?.values?.voicemail_enabled !== true ||
      recordingMode(values.value) !== recordingMode(snapshot.value?.values)),
)
const selectedMode = computed({
  get: () => recordingMode(values.value),
  set: (mode) => {
    values.value.voicemail_upload_mode = mode === 'lan' ? '' : mode
    voicemailConsent.value = false
  },
})
const proxySources = computed({
  get: () => (values.value.voicemail_proxy_cidrs || []).join('\n'),
  set: (text) => {
    values.value.voicemail_proxy_cidrs = text
      .split('\n')
      .map((x) => x.trim())
      .filter(Boolean)
  },
})
const storageMiB = computed({
  get: () => values.value.voicemail_max_storage_mib || 512,
  set: (value) => {
    values.value.voicemail_max_storage_mib = Number(value)
  },
})
const enableProblem = computed(() => recordingEnableProblem(snapshot.value?.values, values.value))
const reception = computed(() => snapshot.value?.voicemail_reception)
const canSave = computed(
  () =>
    snapshot.value &&
    !busy.value &&
    !uncertain.value &&
    !restarting.value &&
    !props.locked &&
    consent.value &&
    !enableProblem.value &&
    (!enablingVoicemail.value || voicemailConsent.value),
)
const editedValues = computed(() =>
  settingsSectionValues(
    snapshot.value?.values || {},
    {
      ...values.value,
      device_cidrs: sources.value
        .split('\n')
        .map((value) => value.trim())
        .filter(Boolean),
    },
    props.section,
  ),
)
const dirty = computed(
  () =>
    snapshot.value &&
    JSON.stringify(normalizedSettingsValues(editedValues.value)) !==
      JSON.stringify(normalizedSettingsValues(snapshot.value.values)),
)
watch(
  () => ({
    revision: snapshot.value?.revision,
    ready:
      !!snapshot.value &&
      !dirty.value &&
      !snapshot.value.pending_restart &&
      !busy.value &&
      !uncertain.value &&
      !restarting.value,
    url: snapshot.value?.values?.voicemail_amr_url || '',
    receivingReady: reception.value?.ready === true,
  }),
  (status) => emit('configuration-status', status),
  { immediate: true },
)
function toggleVoicemailSlot(slot) {
  const slots = new Set(values.value.voicemail_slots || [])
  if (slots.has(slot)) slots.delete(slot)
  else slots.add(slot)
  values.value.voicemail_slots = [...slots].sort()
  voicemailConsent.value = false
}
function fillVoicemailURL() {
  const bytes = new Uint8Array(16)
  crypto.getRandomValues(bytes)
  const token = [...bytes].map((b) => b.toString(16).padStart(2, '0')).join('')
  const origin =
    selectedMode.value === 'public' ? 'https://upload.example.com' : 'http://192.168.31.x:18880'
  values.value.voicemail_amr_url = `${origin}/api/voicemail/amr/${token}?slot={{slot}}&start={{telStartTs}}&phone={{phNum|$urlEncode()}}`
  voicemailConsent.value = false
}
function adopt(data) {
  snapshot.value = data
  values.value = normalizedSettingsValues(data.values)
  sources.value = (data.values.device_cidrs || []).join('\n')
  secrets.value = {}
  consent.value = false
  voicemailConsent.value = false
  restartConsent.value = false
  uncertain.value = false
  if (!data.pending_restart) restarting.value = false
}
async function load() {
  if (busy.value || stopped || props.locked) return
  busy.value = true
  error.value = ''
  secrets.value = {}
  try {
    const data = await flow.load()
    if (data && !stopped) adopt(data)
  } catch (e) {
    if (!stopped) error.value = e.message
  } finally {
    busy.value = false
  }
}
async function save() {
  if (!canSave.value) return
  busy.value = true
  error.value = ''
  note.value = ''
  const replacement = recording.value ? {} : { ...secrets.value }
  secrets.value = {}
  const body = editedValues.value
  try {
    const data = await flow.save(
      body,
      replacement,
      snapshot.value.revision,
      consent.value,
      voicemailConsent.value,
    )
    if (data && !stopped) {
      const turningOff =
        (snapshot.value?.values?.voicemail_enabled === true ||
          props.state?.voicemail_enabled === true) &&
        values.value.voicemail_enabled !== true
      adopt(data)
      note.value = settingsSavedNote(recording.value && turningOff)
      emit('changed')
    }
  } catch (e) {
    if (!stopped) {
      error.value = e.message
      uncertain.value = true
    }
  } finally {
    busy.value = false
    consent.value = false
  }
}
async function restart() {
  if (
    !snapshot.value ||
    !restartConsent.value ||
    busy.value ||
    restarting.value ||
    uncertain.value ||
    dirty.value ||
    props.locked
  )
    return
  busy.value = true
  error.value = ''
  restarting.value = true
  try {
    const result = await flow.restart(snapshot.value.revision, restartConsent.value)
    if (result && !stopped) {
      note.value = '重启请求已提交，正在等待恢复。如果修改了管理凭据，请用新凭据重新登录。'
      pollResume(0)
    }
  } catch (e) {
    if (!stopped) error.value = e.message + ' 请刷新页面确认状态，不要重复提交。'
  } finally {
    busy.value = false
    restartConsent.value = false
  }
}
function pollResume(attempt) {
  poll = setTimeout(async () => {
    if (stopped) return
    try {
      const data = await flow.load()
      if (data && !data.pending_restart) {
        adopt(data)
        note.value = '应用已恢复，当前保存的配置已生效。'
        emit('changed')
        return
      }
    } catch {
      /* Read-only recovery check; never resend restart. */
    }
    if (stopped) return
    if (attempt < 19) pollResume(attempt + 1)
    else note.value = '尚未确认恢复。请刷新页面；若修改了地址或管理凭据，请使用新地址或新凭据。'
  }, 1500)
}
onMounted(load)
onUnmounted(() => {
  stopped = true
  clearTimeout(poll)
  secrets.value = {}
  flow.dispose()
})
</script>
<template>
  <section class="panel compose system-settings">
    <div class="detail-head">
      <div>
        <p class="eyebrow">{{ recording ? 'RECORDING' : 'SETTINGS' }}</p>
        <h2>
          {{ recording ? '录音配置' : '系统配置' }}
          <InfoTip :label="recording ? '录音配置说明' : '系统配置说明'">
            {{
              recording
                ? '第一步设置应用如何接收录音，第二步设置板子是否接听和向哪里上传。分别确认，不会自动联动。'
                : '这里修改应用的通用设置，保存并重启应用后生效。接通提示语、录音接收及板子接听上传请到“录音配置”管理。'
            }}
          </InfoTip>
        </h2>
      </div>
      <span v-if="snapshot" class="badge">{{
        snapshot?.pending_restart ? '配置已保存，重启应用后生效' : '当前配置已生效'
      }}</span>
    </div>
    <p v-if="recording" class="muted">
      先设置应用，再设置板子。上传地址只填一次；两步分别确认，保存应用设置不会修改板子或发送短信。
    </p>
    <p v-if="error" class="notice error" role="alert">{{ error }}</p>
    <p v-if="note" class="notice" role="status">{{ note }}</p>
    <button type="button" :disabled="busy || restarting || locked" @click="load">
      重新读取配置
    </button>
    <form v-if="snapshot" @submit.prevent="save">
      <fieldset :disabled="busy || restarting || locked">
        <div v-for="group in recording ? [] : groups" :key="group.title" class="settings-group">
          <h3>{{ group.title }}</h3>
          <div class="settings-grid">
            <label v-for="field in group.fields" :key="field[0]"
              >{{ field[1]
              }}<input
                v-if="field[2] === 'number'"
                v-model.number="values[field[0]]"
                type="number"
                :min="field[3]"
                :max="field[4]"
                step="1"
                required /><input
                v-else
                v-model="values[field[0]]"
                type="text"
                :required="
                  !['public_url', 'bark_base_url', 'admin_token_header'].includes(field[0])
                "
            /></label>
          </div>
          <p v-if="group.title === '管理网址与时间'" class="tiny muted">
            记录解析与统计时区只影响管理应用，不修改板子的时钟或时区。
          </p>
          <p v-if="group.title === '管理访问'" class="tiny muted">
            与反向代理 Basic Auth 冲突时，可填写 X-SIMHarbor-Authorization。仅填写请求头名称，
            管理凭据不变；保存并重启应用后生效，随后刷新页面。反向代理需透传该请求头。
          </p>
        </div>
        <details v-if="!recording" class="settings-group settings-advanced">
          <summary>
            高级连接设置
            <InfoTip label="高级连接设置说明">
              当前连接正常时无需修改。设备实际编号、连接时间和最后上报时间请在首页或运行诊断查看。
            </InfoTip>
          </summary>
          <div class="settings-grid">
            <label v-for="field in connectionFields" :key="field[0]">
              <span class="field-caption"
                >{{ field[1] }} <InfoTip :label="field[1] + '说明'">{{ field[3] }}</InfoTip></span
              >
              <input v-model="values[field[0]]" type="text" required />
            </label>
            <label>
              <span class="field-caption"
                >无上报多久判定离线（秒）
                <InfoTip label="离线判断说明"
                  >服务端的离线、状态新鲜度和连接超时判断，也影响只读状态续查频率；不修改板子的心跳间隔。设置过短可能频繁断开。</InfoTip
                ></span
              >
              <input
                v-model.number="values.stale_seconds"
                type="number"
                min="5"
                max="86400"
                step="1"
                required
              />
            </label>
          </div>
          <h3>服务端通讯与来源校验</h3>
          <label
            ><span class="field-caption"
              >服务端AES模式
              <InfoTip label="AES模式说明">
                更改Key、IV或模式需要与板子对应，否则会断开。这里不会自动设置板子；仅上行加密的公网传输保护仍未完成。
              </InfoTip></span
            ><select v-model.number="values.aes_mode">
              <option :value="0">不加密</option>
              <option :value="1">仅板子上行加密</option>
              <option :value="2">双向加密（本板加密下行尚未验证通过）</option>
            </select></label
          >
          <label class="consent"
            ><input v-model="values.device_allowlist_enabled" type="checkbox" />启用设备来源IP白名单
            <InfoTip label="来源IP白名单说明">
              关闭时保留原规则，不再按来源IP拒绝连接；仍检查AES和设备编号。关闭白名单不能与“不加密”同时使用。
            </InfoTip></label
          >
          <label
            >允许的来源（每行一个IP网段，单IP用 /32 或 /128）<textarea
              v-model="sources"
              rows="3"
              :disabled="!values.device_allowlist_enabled"
            />
          </label>
        </details>
        <div v-if="!recording" class="settings-group">
          <h3>
            凭据
            <InfoTip label="凭据说明">
              旧值不回显；留空保留，输入新值才替换。更换管理凭据前请先存到密码管理器，重启后重新登录。
            </InfoTip>
          </h3>
          <div class="settings-grid">
            <label v-for="field in secretFields" :key="field[0]"
              >{{ field[1] }} · {{ snapshot.credentials_configured[field[0]] ? '已配置' : '未配置'
              }}<input
                v-model="secrets[field[0]]"
                type="password"
                autocomplete="new-password"
                :placeholder="
                  snapshot.credentials_configured[field[0]] ? '留空保留原值' : '输入后保存'
                "
                maxlength="512"
            /></label>
          </div>
          <label class="consent"
            ><input v-model="secrets.clear_bark" type="checkbox" />清除Bark Key并关闭后续推送</label
          >
        </div>
        <div v-if="recording" class="settings-group">
          <h3>
            1. 应用接收与提示语
            <InfoTip label="留言说明">
              这里确定应用接收录音的地址、目标卡槽和接通提示文字。保存并重启只更新应用，不自动修改板子。下方第二步会使用同一个已生效地址。
            </InfoTip>
          </h3>
          <p class="notice">{{ voicemailStatus }}</p>
          <p v-if="reception" class="tiny muted">
            当前生效的上传方式：{{
              reception.mode === 'public' ? '公网入口（HTTPS）' : '局域网直连（HTTP）'
            }}。保存后的变更需重启应用生效。
          </p>
          <p v-if="!values.voicemail_enabled" class="notice">
            关闭应用并重启后，不再播放提示语或接收新录音，已有录音保留。板子仍可能接听和上传；彻底停用还需执行下方第二步。
          </p>
          <label class="consent"
            ><input
              v-model="values.voicemail_enabled"
              type="checkbox"
              @change="voicemailConsent = false"
            />开启录音接收与接通提示语（保存并重启后生效）</label
          >
          <div class="recording-fields">
            <label
              >上传方式<select v-model="selectedMode">
                <option value="lan">局域网直连（HTTP）</option>
                <option value="public">公网入口（HTTPS）</option>
              </select></label
            >
            <p class="tiny muted">
              切换方式不会替换现有地址或令牌，也不会修改板子。请核对地址后保存。
            </p>
            <div v-if="selectedMode === 'lan'" class="notice">
              <template v-if="reception">
                当前生效的设备来源白名单：{{ reception.allowlist_enabled ? '已开启' : '未开启' }}。
                <template v-if="reception.allowlist_enabled"
                  >允许来源：{{ reception.device_cidrs.join('、') }}。</template
                >
                <template v-else>白名单关闭时，录音上传会被拒绝。</template>
              </template>
              <template v-else>尚未读到当前生效的设备来源白名单，请重新读取配置。</template>
              <span v-if="snapshot.pending_restart"
                >有配置待重启；这里显示的是重启前的实际状态。</span
              >
              如需修改，请到“系统配置 → 高级连接设置”，保存后重启应用，并重新读取本页。
            </div>
            <p v-if="enableProblem" class="notice error" role="alert">{{ enableProblem }}</p>
            <p v-if="selectedMode === 'public'" class="notice">
              板子填写公网 HTTPS 上传地址，Tunnel 可用本机 HTTP
              转发。只开放录音上传路径，不要求邮箱登录；管理页面继续保持保护。
            </p>
            <p class="tiny muted">
              目标卡槽：开启或停用板子设置时使用这些卡槽；留空不会默认修改两张卡。
            </p>
            <div class="settings-grid">
              <label class="consent"
                ><input
                  type="checkbox"
                  :checked="(values.voicemail_slots || []).includes(1)"
                  @change="toggleVoicemailSlot(1)"
                />卡槽 1</label
              >
              <label class="consent"
                ><input
                  type="checkbox"
                  :checked="(values.voicemail_slots || []).includes(2)"
                  @change="toggleVoicemailSlot(2)"
                />卡槽 2</label
              >
            </div>
            <label
              >给来电者播放的提示语<textarea
                v-model="values.voicemail_tts"
                rows="3"
                maxlength="274"
                placeholder="请在提示音后留言"
              />
            </label>
            <p class="tiny muted">接通后只播放一遍，播完不挂断；不会改写板子预设提示语。</p>
            <label
              >板子录音上传地址<input
                v-model="values.voicemail_amr_url"
                type="text"
                maxlength="500"
                :placeholder="
                  selectedMode === 'public'
                    ? 'https://upload.example.com/api/voicemail/amr/…'
                    : 'http://192.168.31.10:18880/api/voicemail/amr/…'
                "
              />
            </label>
            <button v-if="!values.voicemail_amr_url" type="button" @click="fillVoicemailURL">
              生成上传地址模板
            </button>
            <p v-else class="tiny muted">
              现有地址和令牌会保留。修改后请保存并重启应用，再在第二步同步到板子。
            </p>
            <p class="tiny muted">
              {{
                selectedMode === 'public'
                  ? '使用你自己的 HTTPS 上传域名，模板中的 example.com 需替换。'
                  : '使用运行本应用的主机局域网 IP 和端口，模板中的 x 需替换。'
              }}
              地址中的随机令牌就是上传凭据，无需另填。请保密，不要删除关联参数。下方会读取板子地址，按完整地址核对。
            </p>
            <details v-if="selectedMode === 'public'" class="settings-group settings-advanced">
              <summary>公网接收保护</summary>
              <label
                >允许转发录音的代理 IP（每行一个 /32 或 /128）<textarea
                  v-model="proxySources"
                  rows="3"
                  placeholder="留空只允许 127.0.0.1/32 和 ::1/128"
                />
              </label>
              <p class="tiny muted">
                填写应用实际看到的 cloudflared 或反向代理地址，只允许单个内网或本机 IP。Docker
                转发可能显示网桥网关地址；不要填板子公网 IP 或 Cloudflare 公网网段。TCP
                白名单保持独立，不受这里影响。
              </p>
              <label
                >录音目录容量上限（MiB）<input
                  v-model="storageMiB"
                  type="number"
                  min="16"
                  max="65536"
                  step="1"
              /></label>
              <p class="tiny muted">
                默认 512 MiB。达到容量或 10000
                条索引上限后拒收新录音，保留旧文件；重复上传仍去重。单次录音最多 2 MiB，同时接收最多
                2 个请求，允许短时 10 次请求、持续每分钟 60 次。这里不会自动删除录音。已配置 Bark
                时，容量达到一半及此后每增加 32 MiB 会提醒；默认提醒点为 256、288、320
                MiB，依次递增。
              </p>
            </details>
          </div>
          <label v-if="enablingVoicemail" class="consent"
            ><input
              v-model="voicemailConsent"
              type="checkbox"
            />我了解：完成板子设置后，所选卡槽会自动接听并录音，可能产生通话费用，不再只做未接提醒。</label
          >
        </div>
        <div v-if="!recording" class="settings-group">
          <h3>业务选项</h3>
          <label class="consent"
            ><input v-model="values.status_alerts_enabled" type="checkbox" />持续异常与恢复提醒
            <InfoTip label="持续异常提醒规则"
              >板子连续离线15分钟、SIM连续注册异常30分钟，各提醒一次；恢复稳定5分钟后提醒一次，同一故障不重复提醒。查询过期不当SIM异常，板子离线时不同时报SIM故障。应用停机时不能发送提醒；重启重新计时，不补报停机期间故障。两卡推送开关控制短信/来电，此开关独立控制运行异常。</InfoTip
            ></label
          >
          <label class="consent"
            ><input v-model="values.outbound_enabled" type="checkbox" />允许经确认的短信外发</label
          >
          <p class="tiny muted">
            开启不会自动发短信；仍需逐条核对并确认，发送限额与恢复后的禁发保护继续生效。
          </p>
          <label class="consent"
            ><input
              v-model="values.incoming_text_escapes"
              type="checkbox"
            />按板子转义规则显示正文换行</label
          >
          <label
            >日志级别<select v-model="values.log_level">
              <option value="debug">调试</option>
              <option value="info">信息</option>
              <option value="warn">警告</option>
              <option value="error">错误</option>
            </select></label
          >
        </div>
        <div v-if="!recording" class="settings-group settings-paths">
          <h3>
            部署信息（只读）
            <InfoTip label="部署信息说明">
              目录与环境切换涉及部署和数据迁移，不能在这里直接切换到空库或其他文件目录。修改监听地址时也需同步服务器或容器的端口配置。
            </InfoTip>
          </h3>
          <p>环境：{{ values.environment }}</p>
          <p>数据目录：{{ values.data_directory }}</p>
          <p>网页程序目录：{{ values.static_directory }}</p>
        </div>
        <label class="consent"
          ><input v-model="consent" type="checkbox" />{{
            recording
              ? '我已核对卡槽、提示语和上传地址；保存后还需重启应用，板子不会自动改变。'
              : '我已核对参数、凭据和费用限制，了解保存后还需重启应用。'
          }}</label
        >
        <button class="primary" :disabled="!canSave">
          {{ busy ? '处理中…' : recording ? '保存应用录音设置' : '保存配置' }}
        </button>
      </fieldset>
    </form>
    <div v-if="snapshot?.pending_restart" class="settings-group">
      <h3>使保存的配置生效</h3>
      <p v-if="recording" class="tiny muted">
        重启会使所有已保存的应用配置生效，包括“系统配置”页的变更；不会重启板子。
      </p>
      <p v-if="dirty" class="notice">
        还有未保存的编辑。重启只使用已保存配置，请先保存或重新读取配置。
      </p>
      <p class="tiny muted">
        正常重启管理应用，板子连接会短暂中断后自动重连。不会重启板子或自动发送短信。
      </p>
      <label class="consent"
        ><input
          v-model="restartConsent"
          type="checkbox"
          :disabled="busy || restarting || dirty || locked"
        />确认按已保存的配置重启应用</label
      ><button
        :disabled="!restartConsent || busy || restarting || uncertain || dirty || locked"
        @click="restart"
      >
        确认重启应用
      </button>
    </div>
  </section>
</template>
