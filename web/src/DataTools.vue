<script setup>
import InfoTip from './InfoTip.vue'
import { ref, computed, onUnmounted } from 'vue'
import { messageExportQuery, createDownloadFlow, saveFile } from './download.js'
import { cardFilterOptions } from './inbox-cards.js'
const props = defineProps({ fileRequest: Function, state: Object })
const card = ref(''),
  phone = ref(''),
  from = ref(''),
  until = ref(''),
  format = ref('json')
const cardOptions = computed(() => cardFilterOptions(props.state))
const busy = ref(false),
  notice = ref(''),
  failed = ref(false)
const backupConsent = ref(false)
const flow = createDownloadFlow({
  request: (...args) => props.fileRequest(...args),
  save: saveFile,
})
async function exportMessages() {
  if (busy.value) return
  busy.value = true
  notice.value = ''
  failed.value = false
  try {
    const query = messageExportQuery({
      card: card.value,
      phone: phone.value,
      from: from.value,
      until: until.value,
      format: format.value,
    })
    const downloaded = await flow.run('/messages/export?' + query, 'sim-inbox.' + format.value)
    if (downloaded)
      notice.value =
        '已交给浏览器下载。包含卡身份、收件时卡名及全部匹配收件，文件中有完整号码与短信正文，请妥善保存。'
  } catch (e) {
    failed.value = true
    notice.value = e.message
  } finally {
    busy.value = false
  }
}
onUnmounted(() => flow.dispose())
async function backup() {
  if (!backupConsent.value || busy.value) return
  busy.value = true
  notice.value = ''
  failed.value = false
  try {
    if (await flow.run('/backups/download', 'sim-backup.zip', 'POST', { confirmed: true }))
      notice.value = '备份已交给浏览器下载。恢复前需使用本应用校验，恢复仅写入全新目录并阻止外发。'
  } catch (e) {
    failed.value = true
    notice.value = e.message
  } finally {
    busy.value = false
    backupConsent.value = false
  }
}
</script>
<template>
  <div class="outbox">
    <p v-if="notice" class="notice" :class="{ error: failed }" role="status">{{ notice }}</p>
    <section class="panel compose">
      <h2>
        短信导出
        <InfoTip label="短信导出说明"
          >日期使用此浏览器的本地时间。每次最多 10,000 条 / 32
          MiB，超过时请缩小范围；不会只下载前一部分。包含未完整组装的短信并标明状态。CSV
          的公式样式文本会添加保护前缀，原文请使用 JSON。</InfoTip
        >
      </h2>
      <p class="muted">
        导出全部匹配收件，不受收件箱当前页限制。不会发送短信、推送通知或标记已读。
      </p>
      <form class="diagnostic-filters" @submit.prevent="exportMessages">
        <label
          >收件卡<select v-model="card" :disabled="busy">
            <option v-for="option in cardOptions" :key="option.value" :value="option.value">
              {{ option.label }}
            </option>
          </select></label
        >
        <label
          >发件号码（精确匹配）<input
            v-model="phone"
            maxlength="64"
            :disabled="busy"
            placeholder="留空表示全部号码"
        /></label>
        <label>开始时间<input type="datetime-local" v-model="from" :disabled="busy" /></label>
        <label
          >结束时间（不含）<input type="datetime-local" v-model="until" :disabled="busy"
        /></label>
        <label
          >格式<select v-model="format" :disabled="busy">
            <option value="json">JSON · 保留原文</option>
            <option value="csv">CSV · 表格查看</option>
          </select></label
        >
        <div class="outgoing-actions">
          <button type="submit" :disabled="busy">{{ busy ? '正在生成…' : '下载收件短信' }}</button>
        </div>
      </form>
    </section>
    <section class="panel compose">
      <h2>
        数据库备份
        <InfoTip label="数据库备份说明"
          >生成当前数据库的一致快照，包含收件、发件与结果、通知任务及设备状态。不会发送短信或触发新的通知。<br />备份含私人短信、完整号码和设备身份；不包含应用配置、环境变量密钥或日志。请保存在私人位置，另行保管配置与凭据。当前数据库上限为
          256 MiB。<br />安全恢复使用服务器上的维护工具，先校验和预览，再写入全新目录；原数据库保留。网页不覆盖正在运行的数据。</InfoTip
        >
      </h2>
      <p v-if="state?.outbound_restore_blocked" class="notice">
        当前数据库来自恢复，外发已被持久阻止。可查看历史、接收新短信和备份；不能只改配置解除。
      </p>
      <label class="consent"
        ><input
          type="checkbox"
          v-model="backupConsent"
          :disabled="busy"
        />我确认下载并妥善保管私人数据备份</label
      >
      <div class="outgoing-actions">
        <button :disabled="busy || !backupConsent" @click="backup">
          {{ busy ? '正在生成文件…' : '生成并下载备份' }}
        </button>
      </div>
    </section>
  </div>
</template>
