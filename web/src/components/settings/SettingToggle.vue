<script setup lang="ts">
defineProps<{ modelValue: boolean; title: string; description: string; disabled?: boolean }>()
const emit = defineEmits<{ 'update:modelValue': [value: boolean]; change: [] }>()
</script>
<template><label class="setting-toggle" :class="{ disabled }"><div><strong>{{ title }}</strong><span>{{ description }}</span></div><span class="switch"><input type="checkbox" :checked="modelValue" :disabled="disabled" @change="emit('update:modelValue', ($event.target as HTMLInputElement).checked); emit('change')"><span /></span></label></template>
