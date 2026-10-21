<script setup>
import { ref } from 'vue'
import SystemSettings from './SystemSettings.vue'
import BoardVoicemail from './BoardVoicemail.vue'
defineProps({ request: Function, state: Object })
const emit = defineEmits(['changed'])
const appSettings = ref(null)
const boardBusy = ref(false)
</script>
<template>
  <div class="outbox recording-settings">
    <SystemSettings
      section="recording"
      :request="request"
      :state="state"
      :locked="boardBusy"
      @configuration-status="appSettings = $event"
      @changed="emit('changed')"
    />
    <BoardVoicemail
      :request="request"
      :state="state"
      :app-settings="appSettings"
      @busy="boardBusy = $event"
    />
  </div>
</template>
