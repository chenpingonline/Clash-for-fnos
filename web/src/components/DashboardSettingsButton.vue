<script setup lang="ts">
defineProps<{ label: string }>()
const emit = defineEmits<{ click: [] }>()
</script>

<template>
  <button class="dashboard-control-settings" type="button" :aria-label="label" :title="label" @click="emit('click')">
    <svg viewBox="0 0 24 24" aria-hidden="true">
      <path d="M4 8h5m4 0h7M4 16h9m4 0h3" />
      <circle cx="11" cy="8" r="2" />
      <circle cx="15" cy="16" r="2" />
    </svg>
  </button>
</template>
