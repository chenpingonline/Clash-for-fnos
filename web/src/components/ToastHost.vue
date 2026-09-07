<script setup lang="ts">
import { toasts } from '@/services/toast'
</script>

<template>
  <div id="toast" aria-live="polite">
    <div v-for="item in toasts" :key="item.id" class="toast" :class="{ bad: item.bad }">
      {{ item.text }}
    </div>
  </div>
</template>
