<script setup>
import InfoTip from './InfoTip.vue'
import MigrationRecovery from './MigrationRecovery.vue'
import BackupManager from './BackupManager.vue'
import BoardStorage from './BoardStorage.vue'
import { ref, computed, onUnmounted } from 'vue'
import { messageExportQuery, createDownloadFlow, saveFile } from './download.js'
import { cardFilterOptions } from './inbox-cards.js'
const props = defineProps({ fileRequest: Function, request: Function, state: Object })
const emit = defineEmits(['changed'])
const card = ref(''),
  phone = ref(''),
  from = ref(''),
  until = ref(''),
  format = ref('json')
const cardOptions = computed(() => cardFilterOptions(props.state))
const busy = ref(false),
  notice = ref(''),
  failed = ref(false)
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
    <BoardStorage :request="request" :state="state" />
    <BackupManager :request="request" :file-request="fileRequest" :state="state" />
    <MigrationRecovery v-if="request" :request="request" @changed="emit('changed')" />
  </div>
</template>
