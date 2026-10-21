<script setup>
import { ref, onUnmounted } from 'vue'
import InfoTip from './InfoTip.vue'
import BoardHistory from './BoardHistory.vue'
import { createBoardSettingsFlow } from './board-settings-flow.js'
const props = defineProps({ request: Function, state: Object })
const storage = ref(null),
  busy = ref(false),
  error = ref(''),
  historyOpen = ref(false)
const flow = createBoardSettingsFlow({ request: (...args) => props.request(...args) })
async function query() {
  busy.value = true
  error.value = ''
  try {
    const result = await flow.query('storage_query')
    if (result) storage.value = result.storage
  } catch (e) {
    error.value = e.message
  } finally {
    busy.value = false
  }
}
onUnmounted(() => flow.dispose())
</script>
<template>
  <section class="panel compose board-storage">
    <h2>
      板子本地存储
      <InfoTip label="板子存储说明"
        >查询板子配置中的短信与通话存储开关，和应用数据库不同。首批只读，不修改、不清空记录；厂家文档称修改开关须重启板子，读取配置不等于验证运行期间已生效。</InfoTip
      >
    </h2>
    <p v-if="error" class="notice error" role="alert">{{ error }}</p>
    <button type="button" :disabled="busy || !state?.online" @click="query">
      读取短信与通话存储开关
    </button>
    <p v-for="slot in storage ? [1, 2] : []" :key="slot">
      卡槽 {{ slot }}：短信 {{ storage[slot + 's'] ? '开启' : '关闭' }} · 通话
      {{ storage[slot + 't'] ? '开启' : '关闭' }}
    </p>

    <details class="settings-group settings-advanced" @toggle="historyOpen = $event.target.open">
      <summary>板子本地短信与通话历史</summary>
      <BoardHistory v-if="historyOpen" :request="request" :online="state?.online" />
    </details>
  </section>
</template>
