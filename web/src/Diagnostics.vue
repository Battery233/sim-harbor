<script setup>
import InfoTip from './InfoTip.vue'
import BoardHeartbeat from './BoardHeartbeat.vue'
import BoardStatus from './BoardStatus.vue'
import { ref, computed, onMounted, onUnmounted } from 'vue'
import {
  createDiagnosticFlow,
  diagnosticCategories,
  diagnosticOutcomes,
} from './diagnostic-flow.js'
import { diagnosticRows } from './diagnostic-presentation.js'
const props = defineProps({ request: Function, state: Object })
const view = ref({ items: [], data: null, next: 0, loading: false, error: '' })
const level = ref(''),
  category = ref(''),
  reference = ref(''),
  notice = ref('')
const flow = createDiagnosticFlow({
  request: (...args) => props.request(...args),
  changed: (state) => (view.value = state),
})
const filters = () => ({ level: level.value, category: category.value, reference: reference.value })
const names = { debug: '调试', info: '信息', warn: '提醒', error: '错误' }
const events = computed(() => diagnosticRows(view.value.items))
function date(ms) {
  return new Date(ms).toLocaleString('zh-CN', { hour12: false })
}
async function load(more = false) {
  notice.value = ''
  try {
    await flow.load(filters(), more ? view.value.next : 0)
  } catch (e) {
    notice.value = e.message
  }
}
async function download() {
  notice.value = ''
  try {
    const data = await flow.export(filters())
    if (!data) return
    const url = URL.createObjectURL(
      new Blob([JSON.stringify(data, null, 2)], { type: 'application/json' }),
    )
    const link = document.createElement('a')
    link.href = url
    link.download = 'sim-diagnostics-' + new Date().toISOString().replaceAll(':', '-') + '.json'
    document.body.append(link)
    link.click()
    link.remove()
    setTimeout(() => URL.revokeObjectURL(url), 1000)
    notice.value = '已下载当前筛选下的最近诊断记录。它不包含短信数据库或完整操作历史。'
  } catch {
    notice.value = '无法下载诊断文件，请稍后重新读取。'
  }
}
onMounted(() => load())
onUnmounted(() => flow.dispose())
</script>
<template>
  <div class="outbox">
    <p v-if="view.error" class="notice error" role="alert">{{ view.error }}</p>
    <p v-if="notice" class="notice" role="status">{{ notice }}</p>
    <BoardHeartbeat :request="request" :state="state" />
    <BoardStatus :state="state" />
    <section class="panel compose">
      <div class="detail-head">
        <div>
          <p class="eyebrow">DIAGNOSTICS</p>
          <h2>
            运行诊断
            <InfoTip label="运行诊断说明"
              >网页按条数保留最近记录，磁盘定期清理过期日志；容量满时暂停新增磁盘记录，保护保留期内日志。重启尽力恢复最近窗口，不能恢复未落盘记录。调试级别也不记录短信正文、号码、密钥或完整报文。<br />查看与下载只读取记录，不触发板子命令。下载沿用当前筛选，最多保留窗口内的记录；不是数据备份。</InfoTip
            >
          </h2>
        </div>
        <span class="badge" :class="{ success: view.data?.status.file_ok }">{{
          !view.data
            ? '正在读取'
            : view.data.status.file_ok
              ? '日志文件正常'
              : view.data.status.capacity_paused
                ? '容量已满，磁盘记录暂停'
                : view.data.status.file_enabled
                  ? '文件写入异常'
                  : '仅内存记录'
        }}</span>
      </div>
      <p class="muted tiny">
        查看连接原因和命令阶段。最终发送状态请查看发件箱，通知任务请查看“推送记录”。
      </p>
      <div class="notification-summary">
        <span>日志级别 {{ view.data ? names[view.data.status.level] : '—' }}</span>
        <span
          >保留 {{ view.data?.status.retained || 0 }} /
          {{ view.data?.status.limit || '—' }} 条</span
        >
        <span v-if="view.data?.status.retention_days"
          >磁盘保留 {{ view.data.status.retention_days }} 天</span
        >
        <span v-if="view.data?.status.max_disk_bytes"
          >磁盘上限 {{ Math.round(view.data.status.max_disk_bytes / 1048576) }} MiB</span
        >
      </div>
      <form class="diagnostic-filters" @submit.prevent="load(false)">
        <label
          >事件类别<select v-model="category" :disabled="view.loading">
            <option value="">全部类别</option>
            <option v-for="(name, key) in diagnosticCategories" :key="key" :value="key">
              {{ name }}
            </option>
          </select></label
        >
        <label
          >记录级别<select v-model="level" :disabled="view.loading">
            <option value="">全部级别</option>
            <option v-for="(name, key) in names" :key="key" :value="key">{{ name }}</option>
          </select></label
        >
        <details class="diagnostic-reference diagnostic-options">
          <summary>按编号查找（高级）</summary>
          <label
            >关联摘要或发件操作 ID<input
              v-model="reference"
              :disabled="view.loading"
              maxlength="32"
              placeholder="24 位摘要 / 32 位操作 ID"
              spellcheck="false"
              autocapitalize="off"
          /></label>
        </details>
        <div class="outgoing-actions">
          <button type="submit" :disabled="view.loading">
            {{ view.loading ? '读取中…' : '查看记录' }}</button
          ><button type="button" :disabled="view.loading" @click="download">下载最近诊断</button>
        </div>
      </form>
    </section>
    <section class="panel compose">
      <h2>
        最近事件
        <InfoTip label="最近事件说明"
          >这里是过去的记录，当前是否在线请看首页。同一次查询合并展示；短时间相同回执合并计数，回执条数不是短信发送次数。</InfoTip
        >
      </h2>
      <p v-if="!view.items.length" class="empty muted">
        {{ view.loading ? '正在读取…' : '当前筛选没有诊断记录' }}
      </p>
      <ul class="diagnostic-list">
        <li v-for="row in events" :key="row.id">
          <div class="result-line">
            <span class="badge" :class="{ 'diagnostic-warning': row.warning }">{{
              row.status
            }}</span
            ><strong>{{ diagnosticCategories[row.category] || '事件' }}</strong
            ><time class="tiny muted">{{ date(row.time_ms) }}</time>
          </div>
          <p class="diagnostic-title">{{ row.title }}</p>
          <p v-if="row.explanation" class="tiny muted">{{ row.explanation }}</p>
          <p v-if="row.duration_ms != null" class="tiny muted">
            响应耗时 {{ row.duration_ms }} 毫秒
          </p>
          <p v-if="row.kind === 'event' && row.records.length > 1" class="tiny muted">
            收到 {{ row.records.length }} 条相同回执
          </p>
          <details class="diagnostic-details">
            <summary>技术详情（{{ row.records.length }} 条记录）</summary>
            <div v-for="record in row.records" :key="record.id" class="diagnostic-record">
              <p class="tiny muted">{{ date(record.time_ms) }} · {{ names[record.level] }}</p>
              <p>{{ record.message }}</p>
              <p v-if="record.event_type === 501 && record.sms_time" class="tiny muted">
                短信时间 · {{ date(record.sms_time * 1000) }}
              </p>
              <p v-if="record.event_type === 501" class="tiny muted">
                总片数 {{ record.sms_total || '未知' }} · 本次分片
                {{ record.sms_parts || '未提供' }}
                <template v-if="typeof record.sms_complete === 'boolean'">
                  · 板子报告{{ record.sms_complete ? '已收齐' : '未收齐' }}
                </template>
                <template v-if="typeof record.sms_stored_complete === 'boolean'">
                  · 入库后{{ record.sms_stored_complete ? '已收齐' : '未收齐' }}
                </template>
                <template v-if="record.sms_invalid_parts">
                  · 无效分片项 {{ record.sms_invalid_parts }}</template
                >
                <template v-if="record.message_id"> · 短信记录 #{{ record.message_id }}</template>
              </p>
              <p v-if="record.outcome" class="tiny muted">
                {{ diagnosticOutcomes[record.outcome] || '结果类别待核对' }}
              </p>
              <p class="tiny muted">
                {{ record.code
                }}<template v-if="record.event_type">
                  · 消息 {{ record.event_type
                  }}<template v-if="record.sub_type != null"> / {{ record.sub_type }}</template>
                </template>
                <template v-if="record.slot"> · 卡槽 {{ record.slot }}</template>
                <template v-if="record.duration_ms != null">
                  · {{ record.duration_ms }} ms</template
                >
              </p>
              <dl
                v-if="
                  record.session ||
                  record.operation ||
                  record.transaction ||
                  record.sms_group ||
                  record.call_ref ||
                  record.media_ref
                "
                class="diagnostic-refs"
              >
                <div v-if="record.session">
                  <dt>连接摘要</dt>
                  <dd>{{ record.session }}</dd>
                </div>
                <div v-if="record.operation">
                  <dt>发件操作 ID</dt>
                  <dd>{{ record.operation }}</dd>
                </div>
                <div v-if="record.transaction">
                  <dt>事务摘要</dt>
                  <dd>{{ record.transaction }}</dd>
                </div>
                <div v-if="record.sms_group">
                  <dt>匿名短信组</dt>
                  <dd>{{ record.sms_group }}</dd>
                </div>
                <div v-if="record.call_ref">
                  <dt>通话关联摘要</dt>
                  <dd>{{ record.call_ref }}</dd>
                </div>
                <div v-if="record.media_ref">
                  <dt>录音关联摘要</dt>
                  <dd>{{ record.media_ref }}</dd>
                </div>
              </dl>
            </div>
          </details>
        </li>
      </ul>
      <button v-if="view.next" :disabled="view.loading" @click="load(true)">查看更早记录</button>
    </section>
  </div>
</template>
