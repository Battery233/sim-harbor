<script setup>
import { ref, computed } from 'vue'
import InfoTip from './InfoTip.vue'
import { boardStatusView } from './board-history-flow.js'
const props = defineProps({ state: Object })
const showIdentity = ref(false)
const rawStatus = computed(() =>
  JSON.stringify(boardStatusView(props.state?.reported || {}, showIdentity.value), null, 2),
)
</script>
<template>
  <section class="panel compose">
    <details class="settings-group settings-advanced">
      <summary>
        最近一次原始状态
        <InfoTip label="原始状态说明"
          >显示应用最近一次stat读回的状态，不自动发命令；过期时可通过首页查询最新板子状态。身份默认隐藏，凭据字段始终隐藏；此处不保存、导出或修改参数。</InfoTip
        >
      </summary>
      <p class="tiny muted">
        上次查询：{{
          state?.status_received_at
            ? new Date(state.status_received_at * 1000).toLocaleString('zh-CN')
            : '尚未查询'
        }}
      </p>
      <label class="consent"
        ><input v-model="showIdentity" type="checkbox" />显示SIM完整身份信息</label
      >
      <pre class="board-raw-status">{{ rawStatus }}</pre>
    </details>
  </section>
</template>
