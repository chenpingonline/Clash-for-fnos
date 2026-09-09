<script setup lang="ts">
import { computed, nextTick, onBeforeUnmount, onMounted, ref, watch } from 'vue'
import { bucketTrafficSamples, stabilizeTrafficScale, trafficSampleIndexAtTime, trafficScaleMaximum, trafficTooltipLeft, type TrafficScaleState } from '@/services/dashboard'
import { formatRate } from '@/services/format'
import type { TrafficSample } from '@/types/api'

type TrafficRange = 1 | 5 | 10

const props = defineProps<{ up: number; down: number; failed?: boolean; history?: TrafficSample[]; embedded?: boolean }>()
const canvas = ref<HTMLCanvasElement | null>(null)
const samples = ref<TrafficSample[]>([])
const rangeMinutes = ref<TrafficRange>(10)
const activeSampleTime = ref<number | null>(null)
const tooltip = ref<{ time: string; up: string; down: string; left: number; top: number } | null>(null)
let resizeObserver: ResizeObserver | null = null
let themeObserver: MutationObserver | null = null
let keyboardNavigation = false

interface ChartFrame {
  left: number
  right: number
  top: number
  bottom: number
}

interface ChartRenderState {
  width: number
  height: number
  frame: ChartFrame
  plotWidth: number
  plotHeight: number
  windowStart: number
  windowEnd: number
  visible: TrafficSample[]
  upload: string
  download: string
  muted: string
}

let renderState: ChartRenderState | null = null
const scaleStates = new Map<TrafficRange, TrafficScaleState>()

const chartAriaLabel = computed(() => {
  const selected = tooltip.value
  if (!selected) return `最近${rangeMinutes.value}分钟上传与下载速率曲线图，可悬停或使用左右方向键查看详情`
  return `${selected.time}，上传 ${selected.up}，下载 ${selected.down}`
})

function formatTooltipTime(time: number): string {
  return new Date(time).toLocaleTimeString([], {
    hour: '2-digit',
    minute: '2-digit',
    second: '2-digit',
    hour12: false,
  })
}

function pointY(value: number, maximum: number, frame: ChartFrame, plotHeight: number): number {
  return frame.top + (1 - Math.max(0, Math.min(1, value / maximum))) * plotHeight
}

function pointX(time: number, windowStart: number, windowEnd: number, frame: ChartFrame, plotWidth: number): number {
  const ratio = Math.max(0, Math.min(1, (time - windowStart) / Math.max(1, windowEnd - windowStart)))
  return frame.left + ratio * plotWidth
}

function draw() {
  const element = canvas.value
  if (!element) return
  const width = Math.max(320, element.clientWidth)
  const height = Math.max(132, element.clientHeight)
  const ratio = Math.min(2, window.devicePixelRatio || 1)
  element.width = Math.round(width * ratio)
  element.height = Math.round(height * ratio)
  const context = element.getContext('2d')
  if (!context) return
  context.setTransform(ratio, 0, 0, ratio, 0, 0)
  context.clearRect(0, 0, width, height)

  const styles = getComputedStyle(document.documentElement)
  const muted = styles.getPropertyValue('--muted').trim() || '#8f98aa'
  const line = styles.getPropertyValue('--line').trim() || '#242a36'
  const upload = styles.getPropertyValue('--accent2').trim() || '#8f75ff'
  const download = '#4b9cff'
  const now = Date.now()
  const range = rangeMinutes.value
  const windowStart = now - range * 60_000
  const bucketDuration = range === 10 ? 2000 : 1000
  const visible = bucketTrafficSamples(samples.value.filter(item => item.time >= windowStart && item.time <= now), bucketDuration)
  const targetMaximum = trafficScaleMaximum(visible.flatMap(item => [item.up, item.down]))
  const scaleState = stabilizeTrafficScale(scaleStates.get(range), targetMaximum, now)
  scaleStates.set(range, scaleState)
  const maximum = scaleState.maximum

  context.font = '11px Inter, -apple-system, BlinkMacSystemFont, "Segoe UI", sans-serif'
  const tickLabels = Array.from({ length: 5 }, (_, index) => formatRate(maximum * (4 - index) / 4))
  const labelWidth = Math.max(...tickLabels.map(label => context.measureText(label).width))
  const frame = { left: Math.max(58, Math.ceil(labelWidth + 18)), right: 16, top: 12, bottom: 30 }
  const plotWidth = width - frame.left - frame.right
  const plotHeight = height - frame.top - frame.bottom

  renderState = { width, height, frame, plotWidth, plotHeight, windowStart, windowEnd: now, visible, upload, download, muted }

  context.textBaseline = 'middle'
  context.lineWidth = 1
  for (let index = 0; index <= 4; index += 1) {
    const y = frame.top + plotHeight * index / 4
    context.strokeStyle = line
    context.globalAlpha = 0.68
    context.setLineDash([4, 5])
    context.beginPath()
    context.moveTo(frame.left, y)
    context.lineTo(width - frame.right, y)
    context.stroke()
    context.setLineDash([])
    context.globalAlpha = 1
    context.fillStyle = muted
    context.textAlign = 'right'
    context.fillText(tickLabels[index]!, frame.left - 10, y)
  }

  context.fillStyle = muted
  context.textAlign = 'center'
  context.textBaseline = 'top'
  const showSeconds = range === 1
  for (let index = 0; index <= 5; index += 1) {
    const value = windowStart + (now - windowStart) * index / 5
    const label = new Date(value).toLocaleTimeString([], {
      hour: '2-digit',
      minute: '2-digit',
      ...(showSeconds ? { second: '2-digit' } : {}),
      hour12: false,
    })
    context.textAlign = index === 0 ? 'left' : index === 5 ? 'right' : 'center'
    context.fillText(label, frame.left + plotWidth * index / 5, height - frame.bottom + 10)
  }

  const drawSeries = (key: 'up' | 'down', color: string) => {
    if (!visible.length) return
    const points = visible.map(item => ({
      x: pointX(item.time, windowStart, now, frame, plotWidth),
      y: frame.top + (1 - Math.max(0, Math.min(1, item[key] / maximum))) * plotHeight,
    }))
    const traceCurve = () => {
      context.moveTo(points[0]!.x, points[0]!.y)
      for (let index = 1; index < points.length; index += 1) {
        const current = points[index]!
        const next = points[Math.min(index + 1, points.length - 1)]!
        context.quadraticCurveTo(current.x, current.y, (current.x + next.x) / 2, (current.y + next.y) / 2)
      }
      const last = points[points.length - 1]!
      context.lineTo(last.x, last.y)
    }

    const gradient = context.createLinearGradient(0, frame.top, 0, frame.top + plotHeight)
    gradient.addColorStop(0, `${color}2e`)
    gradient.addColorStop(1, `${color}00`)
    context.beginPath()
    traceCurve()
    context.lineTo(points[points.length - 1]!.x, frame.top + plotHeight)
    context.lineTo(points[0]!.x, frame.top + plotHeight)
    context.closePath()
    context.fillStyle = gradient
    context.fill()

    context.beginPath()
    context.strokeStyle = color
    context.lineWidth = 2.4
    context.lineCap = 'round'
    context.lineJoin = 'round'
    traceCurve()
    context.stroke()
  }
  drawSeries('down', download)
  drawSeries('up', upload)

  const selectedTime = activeSampleTime.value
  const selectedIndex = selectedTime === null ? -1 : visible.findIndex(item => item.time === selectedTime)
  const selected = selectedIndex < 0 ? null : visible[selectedIndex]
  if (!selected || !visible.length) {
    tooltip.value = null
    return
  }

  const x = pointX(selected.time, windowStart, now, frame, plotWidth)
  const upY = pointY(selected.up, maximum, frame, plotHeight)
  const downY = pointY(selected.down, maximum, frame, plotHeight)

  context.save()
  context.strokeStyle = muted
  context.globalAlpha = 0.72
  context.lineWidth = 1
  context.setLineDash([5, 5])
  context.beginPath()
  context.moveTo(Math.round(x) + 0.5, frame.top)
  context.lineTo(Math.round(x) + 0.5, frame.top + plotHeight)
  context.stroke()
  context.setLineDash([])
  context.globalAlpha = 1
  for (const [y, color] of [[downY, download], [upY, upload]] as const) {
    context.beginPath()
    context.arc(x, y, 3.5, 0, Math.PI * 2)
    context.fillStyle = color
    context.fill()
    context.strokeStyle = styles.getPropertyValue('--surface').trim() || '#171b24'
    context.lineWidth = 2
    context.stroke()
  }
  context.restore()

  const tooltipHeight = 76
  const desiredTop = Math.min(upY, downY) + 10
  const top = Math.max(frame.top + 6, Math.min(desiredTop, frame.top + plotHeight - tooltipHeight - 6))
  tooltip.value = {
    time: formatTooltipTime(selected.time),
    up: formatRate(selected.up),
    down: formatRate(selected.down),
    left: trafficTooltipLeft(x, width),
    top,
  }
}

function selectSampleAt(clientX: number) {
  const element = canvas.value
  const state = renderState
  if (!element || !state?.visible.length) return
  const bounds = element.getBoundingClientRect()
  const plotRatio = Math.max(0, Math.min(1, (clientX - bounds.left - state.frame.left) / state.plotWidth))
  const targetTime = state.windowStart + plotRatio * (state.windowEnd - state.windowStart)
  const index = trafficSampleIndexAtTime(state.visible, targetTime)
  activeSampleTime.value = state.visible[index]?.time ?? null
  draw()
}

function handlePointerMove(event: PointerEvent) {
  keyboardNavigation = false
  selectSampleAt(event.clientX)
}

function handlePointerLeave() {
  if (keyboardNavigation) return
  hideTooltip()
}

function hideTooltip() {
  activeSampleTime.value = null
  tooltip.value = null
  draw()
}

function handleChartFocus(event: FocusEvent) {
  keyboardNavigation = (event.currentTarget as HTMLElement).matches(':focus-visible')
  if (!keyboardNavigation) return
  const count = renderState?.visible.length || 0
  if (!count) return
  activeSampleTime.value = renderState?.visible[count - 1]?.time ?? null
  draw()
}

function handleChartBlur() {
  keyboardNavigation = false
  hideTooltip()
}

function handleChartKeydown(event: KeyboardEvent) {
  const count = renderState?.visible.length || 0
  if (!count || !['ArrowLeft', 'ArrowRight', 'Home', 'End', 'Escape'].includes(event.key)) return
  event.preventDefault()
  keyboardNavigation = true
  if (event.key === 'Escape') {
    keyboardNavigation = false
    hideTooltip()
    return
  }
  if (event.key === 'Home') activeSampleTime.value = renderState?.visible[0]?.time ?? null
  else if (event.key === 'End') activeSampleTime.value = renderState?.visible[count - 1]?.time ?? null
  else {
    const activeTime = activeSampleTime.value
    const current = activeTime === null ? count - 1 : trafficSampleIndexAtTime(renderState!.visible, activeTime)
    const next = Math.max(0, Math.min(count - 1, current + (event.key === 'ArrowLeft' ? -1 : 1)))
    activeSampleTime.value = renderState?.visible[next]?.time ?? null
  }
  draw()
}

function mergeSamples(incoming: TrafficSample[]) {
  const cutoff = Date.now() - 10 * 60_000
  const merged = new Map<number, TrafficSample>()
  for (const item of [...samples.value, ...incoming]) {
    const time = Number(item.time)
    const up = Number(item.up)
    const down = Number(item.down)
    if (!Number.isFinite(time) || time < cutoff || !Number.isFinite(up) || !Number.isFinite(down)) continue
    merged.set(time, { time, up: Math.max(0, up), down: Math.max(0, down) })
  }
  samples.value = [...merged.values()].sort((left, right) => left.time - right.time).slice(-600)
}

watch(() => props.history, history => {
  mergeSamples(history || [])
  nextTick(draw)
}, { immediate: true })

watch(() => [props.up, props.down] as const, ([up, down]) => {
  mergeSamples([{ time: Date.now(), up: Math.max(0, Number(up || 0)), down: Math.max(0, Number(down || 0)) }])
  nextTick(draw)
}, { immediate: true })

watch(rangeMinutes, () => {
  activeSampleTime.value = null
  tooltip.value = null
  nextTick(draw)
})

onMounted(() => {
  if (canvas.value) {
    resizeObserver = new ResizeObserver(draw)
    resizeObserver.observe(canvas.value)
  }
  themeObserver = new MutationObserver(draw)
  themeObserver.observe(document.documentElement, { attributes: true, attributeFilter: ['data-theme'] })
  nextTick(draw)
})

onBeforeUnmount(() => {
  resizeObserver?.disconnect()
  themeObserver?.disconnect()
})
</script>

<template>
  <section class="dashboard-traffic" :class="{ card: !embedded, embedded }" aria-labelledby="traffic-chart-title">
    <div class="dashboard-traffic-head">
      <h2 id="traffic-chart-title">实时流量</h2>
      <div class="dashboard-traffic-live" aria-live="polite">
        <div><span><i class="upload-dot" aria-hidden="true" />上传</span><strong class="up">{{ failed ? '—' : formatRate(up) }}</strong></div>
        <div><span><i class="download-dot" aria-hidden="true" />下载</span><strong class="down">{{ failed ? '—' : formatRate(down) }}</strong></div>
      </div>
      <div class="traffic-range" aria-label="图表时间范围">
        <button v-for="minutes in ([1, 5, 10] as const)" :key="minutes" type="button" :class="{ active: rangeMinutes === minutes }" :aria-pressed="rangeMinutes === minutes" @click="rangeMinutes = minutes">{{ minutes }} 分钟</button>
      </div>
    </div>
    <div
      class="traffic-chart-stage"
      :aria-label="chartAriaLabel"
      role="img"
      tabindex="0"
      @blur="handleChartBlur"
      @focus="handleChartFocus"
      @keydown="handleChartKeydown"
      @pointerdown="handlePointerMove"
      @pointerleave="handlePointerLeave"
      @pointermove="handlePointerMove"
    >
      <canvas ref="canvas" class="traffic-chart-canvas" aria-hidden="true" />
      <div
        v-if="tooltip"
        class="traffic-chart-tooltip"
        :style="{ left: `${tooltip.left}px`, top: `${tooltip.top}px` }"
        aria-hidden="true"
      >
        <time>{{ tooltip.time }}</time>
        <strong class="up"><span aria-hidden="true">↑</span>{{ tooltip.up }}</strong>
        <strong class="down"><span aria-hidden="true">↓</span>{{ tooltip.down }}</strong>
      </div>
    </div>
  </section>
</template>
