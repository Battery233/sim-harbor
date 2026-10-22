<script setup>
import { ref } from 'vue'
import { deleteRecording } from './voicemail-delete.js'
const props = defineProps({ request: Function, media: String })
const emit = defineEmits(['changed'])
const confirming = ref(false),
  busy = ref(false),
  error = ref('')
function toggleConfirmation() {
  confirming.value = !confirming.value
  error.value = ''
}
async function remove() {
  if (!confirming.value || busy.value) return
  busy.value = true
  error.value = ''
  try {
    await deleteRecording(props.request, props.media, true)
    confirming.value = false
  } catch (e) {
    error.value = e.message || '删除结果未确认，请刷新列表核对，不会自动重试。'
    confirming.value = false
  } finally {
    busy.value = false
    emit('changed', error.value)
    window.dispatchEvent(new Event('simharbor:recording-deleted'))
  }
}
</script>
<template>
  <span v-if="media" class="recording-delete">
    <button
      type="button"
      class="recording-trash"
      title="删除这段录音"
      aria-label="删除这段录音"
      :disabled="busy"
      @click="toggleConfirmation"
    >
      🗑️
    </button>
    <span v-if="confirming" class="recording-delete-confirm" role="group" aria-label="确认删除录音">
      <span>永久删除这段录音？保留通话记录和已发送通知，取消尚未发送的留言提醒。</span>
      <button type="button" class="danger" :disabled="busy" @click="remove">
        {{ busy ? '删除中…' : '确认删除' }}
      </button>
      <button type="button" :disabled="busy" @click="confirming = false">取消</button>
    </span>
    <span v-if="error" class="tiny" role="alert">{{ error }} 请刷新列表核对。</span>
  </span>
</template>
