<script setup>
import { ref, watch, onUnmounted } from 'vue'
import InfoTip from './InfoTip.vue'
import { createBoardSettingsFlow } from './board-settings-flow.js'
const props = defineProps({ request: Function, state: Object })
const seconds = ref(null),
  target = ref(110),
  busy = ref(false),
  error = ref(''),
  note = ref(''),
  consent = ref(false),
  uncertain = ref(false)
const flow = createBoardSettingsFlow({ request: (...args) => props.request(...args) })
watch(target, () => (consent.value = false))
async function query() {
  busy.value = true
  error.value = ''
  consent.value = false
  try {
    const result = await flow.query('heartbeat_query')
    if (result) {
      seconds.value = result.seconds
      target.value = result.seconds
      uncertain.value = false
    }
  } catch (e) {
    error.value = e.message
  } finally {
    busy.value = false
  }
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
  <section class="panel compose board-heartbeat">
    <h2>
      板子心跳间隔
      <InfoTip label="板子心跳说明"
        >控制板子发送998心跳的间隔，修改后立即生效。服务器会重新查询原值，只有与你确认的原值一致且连接未变才尝试写一次，随后读回确认。为避免断开，不能设置超过服务端连接超时；通常保留当前值即可。</InfoTip
      >
    </h2>
    <p v-if="error" class="notice error" role="alert">{{ error }}</p>
    <p v-if="note" class="notice" role="status">{{ note }}</p>
    <p>当前：{{ seconds === null ? '尚未查询' : seconds + ' 秒' }}</p>
    <button type="button" :disabled="busy || !state?.online" @click="query">
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
  </section>
</template>
