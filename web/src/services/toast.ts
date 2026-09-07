import { readonly, ref } from 'vue'

export interface ToastMessage {
  id: number
  text: string
  bad: boolean
}

const items = ref<ToastMessage[]>([])
let sequence = 0

export function notify(text: string, bad = false): void {
  const id = ++sequence
  items.value.push({ id, text, bad })
  window.setTimeout(() => {
    items.value = items.value.filter(item => item.id !== id)
  }, 3200)
}

export const toasts = readonly(items)
