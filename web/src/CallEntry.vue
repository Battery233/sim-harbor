<script setup>
import { messageSource } from './inbox-cards.js'
import { callStates, callerLabel, ringingLabel, callPushLabel } from './call-history.js'
const props = defineProps({
  call: Object,
  cards: Array,
  highlighted: Boolean,
  onDownload: Function,
  onPlay: Function,
  playback: Object,
})
function playLabel() {
  if (props.playback?.id !== props.call.id) return '播放留言'
  return props.playback.phase === 'playing' ? '停止播放' : '取消播放'
}
function date(ts) {
  return ts ? new Date(ts * 1000).toLocaleString('zh-CN', { hour12: false }) : '未报告'
}
</script>
<template>
  <article class="call-row" :class="{ 'linked-call': highlighted }">
    <div class="detail-head">
      <h3>{{ callerLabel(call.phone) }}</h3>
      <span class="badge" :class="{ 'missed-call': call.state === 'missed' }">{{
        callStates[call.state]
      }}</span>
    </div>
    <p class="tiny muted">
      {{ messageSource(call, cards) }} · 来电时卡槽 {{ call.slot
      }}<span v-if="highlighted"> · 通知对应记录</span>
    </p>
    <dl class="call-times">
      <div>
        <dt>来电开始</dt>
        <dd>{{ date(call.started_at) }}</dd>
      </div>
      <div>
        <dt>来电结束</dt>
        <dd>{{ date(call.ended_at) }}</dd>
      </div>
      <div>
        <dt>
          {{ ['answered', 'connected'].includes(call.state) ? '接通前等待' : '振铃时长' }}
        </dt>
        <dd>{{ ringingLabel(call) }}</dd>
      </div>
    </dl>
    <p v-if="call.state === 'unknown'" class="tiny muted">
      报告不完整、结束未确认或信息有冲突，不能据此判断未接和振铃时长。
    </p>
    <p v-if="call.notification_kind === 'timeout'" class="tiny muted">
      已生成结束报告超时兜底提醒；后续报告只补齐这条记录，不新增第二条提醒。
    </p>
    <p v-if="!call.started_at && call.received_at" class="tiny muted">
      最近报告收到于 {{ date(call.received_at) }}，这不是已确认的来电开始时间。
    </p>
    <p v-if="call.voicemail_id" class="tiny voicemail-actions">
      <button type="button" :aria-pressed="playback?.id === call.id" @click="onPlay(call)">
        {{ playLabel() }}
      </button>
      <button type="button" @click="onDownload(call)">下载 AMR</button>
      <span class="muted"
        >{{ call.voicemail_bytes }} 字节。下载仍是原始
        AMR。若提示录音文件不在，说明只恢复了索引，需要把 voicemail/ 目录单独放回。</span
      >
    </p>
    <p class="tiny" :class="{ 'call-push-sent': call.notification_state === 'sent' }">
      {{ callPushLabel(call)
      }}<span v-if="call.notification_accepted_at">
        · {{ date(call.notification_accepted_at) }}</span
      >
    </p>
  </article>
</template>
