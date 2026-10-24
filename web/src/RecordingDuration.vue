<script setup>
import { ref, watch, onUnmounted } from 'vue'
import { createRecordingDuration } from './recording-duration.js'
const props = defineProps({ request: Function, media: String })
const label = ref('总时长未知')
const duration = createRecordingDuration({
  request: (...args) => props.request(...args),
  changed: (value) => (label.value = value),
})
watch(
  () => props.media,
  (media) => duration.load(media),
  { immediate: true },
)
onUnmounted(() => duration.dispose())
</script>
<template>
  <span>{{ label }}</span>
</template>
