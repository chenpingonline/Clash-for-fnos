<script setup lang="ts">
const props = withDefaults(defineProps<{ open: boolean; title: string; closable?: boolean }>(), { closable: true })
const emit = defineEmits<{ close: [] }>()
</script>

<template>
  <Teleport to="body">
    <div v-if="open" class="modal" role="presentation" @click.self="props.closable && emit('close')">
      <section class="modal-card" role="dialog" aria-modal="true" :aria-label="title">
        <slot name="header"><h3>{{ title }}</h3></slot>
        <slot />
      </section>
    </div>
  </Teleport>
</template>
