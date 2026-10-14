<script setup>
import { ref, computed, onUnmounted } from 'vue'
import InfoTip from './InfoTip.vue'
import { createCardPushFlow } from './card-push-flow.js'
const props = defineProps({ state: Object, request: Function })
const emit = defineEmits(['changed'])
const busy = ref(false),
  error = ref(''),
  uncertain = ref(false),
  note = ref('')
const cards = computed(() =>
  (props.state?.cards || [])
    .filter((c) => c.current_slot > 0)
    .sort((a, b) => a.current_slot - b.current_slot),
)
const flow = createCardPushFlow({
  request: (...args) => props.request(...args),
  onChanged: (state) => emit('changed', state),
})
async function toggle(card) {
  const enabled = !card.notification_enabled
  busy.value = true
  error.value = ''
  note.value = '保存中…'
  try {
    const result = await flow.set(card, enabled)
    if (result) note.value = '已自动保存'
  } catch (e) {
    error.value = e.message
    note.value = ''
    uncertain.value = true
  } finally {
    busy.value = false
  }
}
async function reload() {
  busy.value = true
  try {
    await flow.reload()
    error.value = ''
    uncertain.value = false
  } catch (e) {
    error.value = e.message
  } finally {
    busy.value = false
  }
}
onUnmounted(() => flow.dispose())
</script>
<template>
  <div class="card-push-controls">
    <span class="tiny muted"
      >按卡推送
      <InfoTip label="按卡推送说明"
        >同时控制这张卡的短信和来电提醒，开关跟随卡身份，换槽后保留。关闭继续保存记录，取消尚未发送的通知；重新开启不补推旧消息。已经开始发送的通知可能仍到达，关闭不撤回已发送通知。故障恢复held任务不会因开启而释放。</InfoTip
      ></span
    >
    <div v-for="card in cards" :key="card.id" class="push-switch-row">
      <span>卡槽 {{ card.current_slot }} · {{ card.name || 'SIM' }}</span>
      <button
        type="button"
        role="switch"
        class="push-switch"
        :class="{ on: card.notification_enabled }"
        :aria-checked="card.notification_enabled === true"
        :aria-label="'卡槽 ' + card.current_slot + ' · ' + (card.name || 'SIM') + ' 推送'"
        :disabled="busy || uncertain || !state?.card_locations_verified"
        @click="toggle(card)"
      >
        <span class="push-switch-knob"></span>
      </button>
    </div>
    <span class="tiny muted" role="status">{{ note || '自动保存，即时生效' }}</span>
    <span v-if="!cards.length" class="tiny muted">状态查询确认卡身份后可设置</span>
    <p v-if="error" class="notice error" role="alert">{{ error }} 请重新读取，不要重复切换。</p>
    <button v-if="uncertain" type="button" :disabled="busy" @click="reload">
      重新确认开关状态
    </button>
  </div>
</template>
