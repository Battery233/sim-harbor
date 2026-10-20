<script setup>
import InfoTip from './InfoTip.vue'
import { ref, computed, onMounted, onUnmounted } from 'vue'
import { createSettingsFlow } from './settings-flow.js'
const props = defineProps({ request: Function })
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
const enablingVoicemail = computed(
  () =>
    values.value.voicemail_enabled === true && snapshot.value?.values?.voicemail_enabled !== true,
)
const canSave = computed(
  () =>
    snapshot.value &&
    !busy.value &&
    !uncertain.value &&
    !restarting.value &&
    consent.value &&
    (!enablingVoicemail.value || voicemailConsent.value),
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
  values.value.voicemail_amr_url = `http://192.168.31.x:18880/api/voicemail/amr/${token}?slot={{slot}}&start={{telStartTs}}&phone={{phNum|$urlEncode()}}`
  voicemailConsent.value = false
}
function adopt(data) {
  snapshot.value = data
  values.value = {
    ...data.values,
    admin_token_header: data.values.admin_token_header || '',
    device_allowlist_enabled: data.values.device_allowlist_enabled !== false,
    status_alerts_enabled: data.values.status_alerts_enabled !== false,
  }
  sources.value = (data.values.device_cidrs || []).join('\n')
  secrets.value = {}
  consent.value = false
  voicemailConsent.value = false
  restartConsent.value = false
  uncertain.value = false
  if (!data.pending_restart) restarting.value = false
}
async function load() {
  if (busy.value || stopped) return
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
  const replacement = { ...secrets.value }
  secrets.value = {}
  const body = {
    ...values.value,
    device_cidrs: sources.value
      .split('\n')
      .map((v) => v.trim())
      .filter(Boolean),
  }
  try {
    const data = await flow.save(
      body,
      replacement,
      snapshot.value.revision,
      consent.value,
      voicemailConsent.value,
    )
    if (data && !stopped) {
      adopt(data)
      note.value = '配置已保存。确认后重启管理应用使其生效；板子配置不会被自动修改。'
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
  if (!snapshot.value || !restartConsent.value || busy.value || restarting.value || uncertain.value)
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
        <p class="eyebrow">SETTINGS</p>
        <h2>
          系统配置
          <InfoTip label="系统配置说明"
            >这里修改的是管理应用的参数，不是板子的内部设置；保存不会向板子下发配置命令。参数和凭据保存在同一个私有文件，确认重启应用后生效，短信与来电历史保留。</InfoTip
          >
        </h2>
      </div>
      <span class="badge">{{ snapshot?.pending_restart ? '已保存，待重启生效' : '当前配置' }}</span>
    </div>
    <p v-if="error" class="notice error" role="alert">{{ error }}</p>
    <p v-if="note" class="notice" role="status">{{ note }}</p>
    <button type="button" :disabled="busy || restarting" @click="load">重新读取配置</button>
    <form v-if="snapshot" @submit.prevent="save">
      <fieldset :disabled="busy || restarting">
        <div v-for="group in groups" :key="group.title" class="settings-group">
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
        <details class="settings-group settings-advanced">
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
        <div class="settings-group">
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
        <div class="settings-group">
          <h3>业务选项</h3>
          <h3>
            留言（默认关闭）
            <InfoTip label="留言说明">
              关闭时只记录来电，不接听、不拒接、不拨号。开启并重启后，还需在板子设置确认下发自动接听和上传地址。接通后
              Harbor
              向该卡槽发送一次提示音，播完不挂断。这与“只提醒、不接听”互斥，可能产生通话费用。录音上传只在本地部署使用，云端部署不能代替这块能力。上传地址必须是板子能访问的局域网
              HTTP，不能使用公网域名。接听并录音从接听起就开始，提示音可能和留言在同一段录音里。
            </InfoTip>
          </h3>
          <label class="consent"
            ><input
              v-model="values.voicemail_enabled"
              type="checkbox"
              @change="voicemailConsent = false"
            />允许所选卡槽自动接听并留言</label
          >
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
            >接通后播放一次的提示音<textarea
              v-model="values.voicemail_tts"
              rows="3"
              maxlength="274"
              placeholder="请在提示音后留言"
            />
          </label>
          <label
            >录音上传地址（局域网 HTTP）<input
              v-model="values.voicemail_amr_url"
              type="text"
              maxlength="500"
              placeholder="http://192.168.31.10:18880/api/voicemail/amr/…"
            />
          </label>
          <button type="button" @click="fillVoicemailURL">填入局域网模板</button>
          <p class="tiny muted">
            把模板里的 x
            换成这台管理主机的局域网地址，并核对发布端口。保存不会改板子；重启后到“板子设置”读取、确认并读回。
          </p>
          <label v-if="enablingVoicemail" class="consent"
            ><input
              v-model="voicemailConsent"
              type="checkbox"
            />我确认开启留言：所选卡槽会自动接听并录音，可能产生通话费用，且不再只做未接提醒。</label
          >
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
        <div class="settings-group settings-paths">
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
          ><input
            v-model="consent"
            type="checkbox"
          />我已核对参数、凭据和费用限制；保存不会立即切换运行配置。</label
        >
        <button class="primary" :disabled="!canSave">{{ busy ? '处理中…' : '保存配置' }}</button>
      </fieldset>
    </form>
    <div v-if="snapshot?.pending_restart" class="settings-group">
      <h3>使保存的配置生效</h3>
      <p class="tiny muted">
        正常重启管理应用，板子连接会短暂中断后自动重连。不会重启板子或自动发送短信。
      </p>
      <label class="consent"
        ><input
          v-model="restartConsent"
          type="checkbox"
          :disabled="busy || restarting"
        />确认按已保存的配置重启应用</label
      ><button :disabled="!restartConsent || busy || restarting || uncertain" @click="restart">
        确认重启应用
      </button>
    </div>
  </section>
</template>
