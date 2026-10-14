<script setup>
import { computed } from 'vue'
import { cardLocation, cardNumber, cardDisambiguator } from './inbox-cards.js'
const props = defineProps({ state: Object, now: Number })
const emit = defineEmits(['view-inbox', 'view-calls'])
const records = computed(() => props.state?.cards || [])
</script>

<template>
  <section class="panel card-record-panel">
    <div class="detail-head">
      <h2>卡记录</h2>
      <span class="tiny muted">已识别 {{ records.length }} 张卡</span>
    </div>
    <p class="muted">应用识别过的卡会保留，取出或换槽不会清掉。名称和号码为最近保存的信息。</p>
    <p v-if="!records.length" class="empty muted">暂无已识别的卡。板子在线后可查询状态。</p>
    <ul v-else class="card-record-list">
      <li v-for="card in records" :key="card.id">
        <div class="detail-head">
          <h3>{{ card.name || '未命名卡' }}</h3>
          <span class="badge">{{ cardLocation(state, card, now) }}</span>
        </div>
        <p class="sim-number">
          <span class="tiny muted">保存的号码</span
          ><strong>{{ cardNumber(card) || '未记录号码' }}</strong>
        </p>
        <p class="tiny muted">
          可见收件 {{ card.message_count ?? '—' }} 条<span v-if="!cardNumber(card) && card.tail">
            · SIM编号尾号 {{ card.tail }}</span
          >
          <span v-if="cardDisambiguator(records, card)">
            · 记录编号 {{ cardDisambiguator(records, card) }}</span
          >
        </p>
        <div class="card-record-actions">
          <button @click="emit('view-inbox', card.id)">查看这张卡的短信</button>
          <button @click="emit('view-calls', card.id)">查看这张卡的通话</button>
        </div>
      </li>
    </ul>
    <p class="tiny muted">
      这里不是完整插拔日志。未被应用识别的卡不会补录；历史短信和来电保留当时的卡名。
    </p>
  </section>
</template>
