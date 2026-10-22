<script setup>
import { reactive, ref, computed, onMounted, onUnmounted } from 'vue'
import InfoTip from './InfoTip.vue'
import { createBackupFlow, selectBackupFile } from './backup-flow.js'
import { backupErrors } from './backup-errors.js'
import { saveFile } from './download.js'
const props = defineProps({ request: Function, fileRequest: Function, state: Object })
const current = reactive({
  data: null,
  creating: false,
  downloading: '',
  deleting: '',
  uncertain: false,
  error: '',
  note: '',
})
const destination = ref('browser'),
  consent = ref(false),
  deleteID = ref('')
const flow = createBackupFlow({
  request: (...args) => props.request(...args),
  fileRequest: (...args) => props.fileRequest(...args),
  save: saveFile,
  selectFile: selectBackupFile,
  changed: (value) => Object.assign(current, value),
})
const running = computed(() => current.data?.job?.state === 'running')
const working = computed(() => current.creating || !!current.downloading || !!current.deleting)
const jobProblem = computed(() =>
  current.data?.job?.state === 'failed'
    ? backupErrors[current.data.job.error] || '上次备份未完成，请核对文件列表。'
    : '',
)
function bytes(n) {
  return `${(n / 1024 / 1024).toFixed(1)} MiB`
}
function date(n) {
  return new Date(n * 1000).toLocaleString('zh-CN', { hour12: false })
}
async function start() {
  deleteID.value = ''
  await flow.start(destination.value, consent.value)
  consent.value = false
  await flow.load()
}
async function remove(file) {
  if (deleteID.value !== file.id) {
    deleteID.value = file.id
    return
  }
  await flow.remove(file, true)
  deleteID.value = ''
}
let timer
onMounted(() => {
  flow.load()
  timer = setInterval(() => {
    if (running.value || current.uncertain) flow.load()
  }, 3000)
})
onUnmounted(() => {
  clearInterval(timer)
  flow.dispose()
})
</script>
<template>
  <section class="panel compose backup-manager">
    <h2>
      数据库备份
      <InfoTip label="数据库备份说明"
        >在线生成一致快照，不发送短信或触发通知。数据库上限 512 MiB，ZIP 上限 544 MiB。ZIP
        包含业务数据和留言索引，不包含 AMR
        录音、应用配置、密钥或日志。恢复仍需使用维护工具，写入全新目录。</InfoTip
      >
    </h2>
    <p v-if="state?.outbound_restore_blocked" class="notice">
      当前数据库来自恢复，外发仍被阻止。保存当前备份后，按下方迁移确认流程核对。
    </p>
    <p class="muted">
      选择将 ZIP
      下载到自己的电脑，或保存在运行应用的服务器上。备份含私人数据，文件未加密，请妥善保管。
    </p>
    <div class="backup-controls">
      <label
        >保存方式<select v-model="destination" :disabled="working || running || current.uncertain">
          <option value="browser">下载到浏览器</option>
          <option value="server">保存到服务器</option>
        </select></label
      >
      <p class="tiny muted" v-if="destination === 'browser'">
        支持直接写入文件的浏览器会先让你选择保存位置；其他浏览器使用普通下载，较大文件会占用更多内存。服务器临时副本保留
        1 小时，中断后可重试。
      </p>
      <p class="tiny muted" v-else>
        关闭页面后仍会生成，完成后长期保留，可再下载或手动删除。备份与应用在同一台机器上，重要文件建议再下载另存。
      </p>
      <label class="consent"
        ><input
          type="checkbox"
          v-model="consent"
          :disabled="working || running || current.uncertain"
        />我确认生成并妥善保管私人数据备份</label
      >
      <div class="outgoing-actions">
        <button
          :disabled="!current.data || !consent || working || running || current.uncertain"
          @click="start"
        >
          {{
            current.creating || running
              ? '正在生成备份…'
              : destination === 'server'
                ? '生成并保存到服务器'
                : '生成并下载备份'
          }}
        </button>
        <button class="secondary" :disabled="current.creating" @click="flow.load">
          刷新任务与文件
        </button>
        <button v-if="current.downloading" class="secondary" @click="flow.cancelDownload">
          取消下载
        </button>
      </div>
    </div>
    <p v-if="current.error" class="notice error" role="alert">{{ current.error }}</p>
    <p v-else-if="jobProblem" class="notice error" role="alert">{{ jobProblem }}</p>
    <p v-if="current.note" class="notice" role="status">{{ current.note }}</p>
    <p v-if="running" class="notice" role="status">
      服务器正在生成备份。离开页面不会中止；应用重启会中止未完成任务，不会自动重跑。
    </p>
    <div v-if="current.uncertain" class="outgoing-actions">
      <button class="secondary" @click="flow.acknowledge">已核对列表，允许重新操作</button>
    </div>
    <p class="tiny muted">
      ZIP 不含通话录音。需要保留录音时，请另行保存数据目录中的
      voicemail/。下载完成提示不表示录音已备份。
    </p>
    <template v-if="current.data">
      <h3>服务器上的备份</h3>
      <p class="tiny muted backup-location">
        位置：{{ current.data.directory }}<br />已占用 {{ bytes(current.data.storage_bytes) }} /
        {{ bytes(current.data.max_storage_bytes) }}，最多
        {{ current.data.max_files }}
        份（包含临时副本）。达到上限时停止新增，不自动删除长期备份。容量可在“系统配置 →
        服务器本地备份”调整。
      </p>
      <p v-if="!current.data.items.length" class="muted">暂无备份文件。</p>
      <ul v-else class="backup-list">
        <li v-for="file in current.data.items" :key="file.id">
          <div class="backup-file-info">
            <strong>{{ file.name }}</strong
            ><span class="tiny muted"
              >{{ date(file.created_at) }} · {{ bytes(file.bytes) }} ·
              {{ file.expires_at ? `临时保留至 ${date(file.expires_at)}` : '长期保留' }}</span
            ><span class="tiny muted backup-hash">SHA-256：{{ file.sha256 }}</span>
          </div>
          <div class="outgoing-actions">
            <button
              class="secondary"
              :disabled="working || current.uncertain"
              @click="flow.download(file)"
            >
              {{ current.downloading === file.id ? '正在下载…' : '下载' }}
            </button>
            <button
              class="secondary"
              :disabled="working || running || current.uncertain"
              @click="remove(file)"
            >
              {{ deleteID === file.id ? '确认删除这份服务器备份' : '删除' }}
            </button>
            <button v-if="deleteID === file.id" class="secondary" @click="deleteID = ''">
              取消
            </button>
          </div>
          <p v-if="deleteID === file.id" class="tiny muted">
            只删除服务器上的这份 ZIP，不影响正在使用的数据库。请先确认已有需要保留的副本。
          </p>
        </li>
      </ul>
    </template>
  </section>
</template>
