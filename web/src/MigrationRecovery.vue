<script setup>
import { reactive, ref, onMounted, onUnmounted } from 'vue'
import { createMigrationFlow } from './migration-flow.js'
const props = defineProps({ request: { type: Function, required: true } })
const emit = defineEmits(['changed'])
const current = reactive({ status: null, preview: null, busy: false, uncertain: false, error: '' })
const checks = reactive({
  source_stopped: false,
  history_complete: false,
  unknown_acknowledged: false,
  backup_saved: false,
})
const confirmed = ref(false)
const flow = createMigrationFlow({
  request: (...args) => props.request(...args),
  changed: (value) => Object.assign(current, value),
})
async function load() {
  confirmed.value = false
  const found = await flow.load()
  if (found && !found.blocked) emit('changed')
}
async function preview() {
  confirmed.value = false
  await flow.preview(checks)
}
async function resume() {
  if (await flow.confirm(checks, confirmed.value)) {
    confirmed.value = false
    emit('changed')
  }
}
onMounted(() => flow.load())
onUnmounted(() => flow.dispose())
</script>
<template>
  <section
    v-if="current.status?.restored || current.error"
    class="panel compose migration-recovery"
  >
    <h2>迁移确认与外发恢复</h2>
    <p v-if="current.error" class="notice error" role="alert">{{ current.error }}</p>
    <template v-if="current.status?.blocked">
      <p class="muted">
        当前数据库来自备份恢复。只有核对完整发送历史后，才能解除恢复保护；此操作不会发送短信。
      </p>
      <p class="notice">
        结果未确认的旧短信发送（含板子设置短信）：{{ current.status.unknown_outgoing }}
        条。它们会保留原状态，不自动重发；旧通知也不会补推。
      </p>
      <p class="tiny muted">
        先使用上方“数据库备份”保存当前备份，再在 15
        分钟内完成以下核对。应用重启或发送历史变化后需重新备份与预览。
      </p>
      <fieldset :disabled="current.busy || current.uncertain">
        <label class="consent"
          ><input
            type="checkbox"
            v-model="checks.source_stopped"
          />原实例已停止外发，之后只使用当前实例管理发送</label
        >
        <label class="consent"
          ><input
            type="checkbox"
            v-model="checks.history_complete"
          />已核对备份之后的完整发送历史，确认没有遗漏的发送尝试</label
        >
        <label class="consent"
          ><input
            type="checkbox"
            v-model="checks.unknown_acknowledged"
          />已核对未知结果，不会把它们当作失败重新发送</label
        >
        <label class="consent"
          ><input
            type="checkbox"
            v-model="checks.backup_saved"
          />已下载并妥善保存当前数据库备份与独立配置副本</label
        >
        <p class="tiny muted">
          这些条件由操作者确认，应用无法检查另一台主机或补齐缺失历史；不能确认时请保持保护。
        </p>
        <button :disabled="!Object.values(checks).every(Boolean)" @click="preview">
          预览恢复外发
        </button>
        <div v-if="current.preview" class="settings-group">
          <h3>核对后恢复</h3>
          <p>确认后立即解除恢复保护。发送限额、卡身份校验和逐条确认仍然生效。</p>
          <p class="tiny muted">
            原来的取消记录、未知结果和待核对通知保持不变；系统配置中的外发开关仍需单独开启并使其生效。
          </p>
          <label class="consent"
            ><input
              type="checkbox"
              v-model="confirmed"
            />我确认上述条件，解除恢复保护，不发送短信</label
          >
          <button
            class="primary"
            :disabled="!confirmed || !Object.values(checks).every(Boolean)"
            @click="resume"
          >
            确认解除恢复保护
          </button>
        </div>
      </fieldset>
    </template>
    <p v-else-if="current.status?.confirmed_at" class="notice">
      迁移已确认，恢复保护已解除。此操作没有发送短信；如需发送，仍须启用外发并逐条预览确认。
    </p>
    <button :disabled="current.busy" @click="load">
      {{ current.busy ? '正在处理…' : '重新读取迁移状态' }}
    </button>
    <p v-if="current.uncertain" class="tiny muted">
      上次结果未确认，已停止写入。请重新读取状态，不要重复提交。
    </p>
  </section>
</template>
