<script setup lang="ts">
import { computed } from 'vue'
import { formatEndpoint, type Protocol } from '../api'

const props = defineProps<{
  protocol: Protocol
  listenAddress: string
  listenPort: string
  targetAddress: string
  targetPort: string
}>()

function preview(address: string, port: string) {
  const host = address.trim()
  if (!host) return port ? '待填地址 · ' + port : '待填写地址和端口'
  return port ? formatEndpoint(host, port) : (host === '*' ? '全部 IPv4 / IPv6' : host) + ' · 待填端口'
}

const entry = computed(() => preview(props.listenAddress, props.listenPort))
const target = computed(() => preview(props.targetAddress, props.targetPort))
const filled = computed(() =>
  props.listenAddress.trim() && props.targetAddress.trim() &&
  [props.listenPort, props.targetPort].every(port => /^\d{1,5}$/.test(port) && Number(port) > 0 && Number(port) <= 65535),
)
const pulseKey = computed(() => props.protocol + entry.value + target.value)
</script>

<template>
  <section class="flow" aria-labelledby="flow-title" :data-ready="Boolean(filled)">
    <header class="flow-header">
      <div>
        <h2 id="flow-title">流量走向</h2>
        <p>连接经过 NAS 后，送到你填写的服务。</p>
      </div>
      <span class="protocol-badge">{{ protocol.toUpperCase() }}</span>
    </header>
    <ol class="flow-steps">
      <li class="flow-step flow-origin">
        <span class="flow-symbol" aria-hidden="true">
          <svg class="icon" viewBox="0 0 24 24"><rect x="3" y="4.5" width="18" height="12" rx="2" /><path d="M8 20h8m-4-3.5V20" /></svg>
        </span>
        <span class="flow-copy">
          <span class="flow-caption">外部访问</span>
          <span class="flow-endpoint">客户端</span>
        </span>
      </li>
      <li class="flow-step flow-entry">
        <span class="flow-connector" aria-hidden="true">
          <span v-if="filled" :key="pulseKey" class="flow-packet"></span>
        </span>
        <span class="flow-symbol" aria-hidden="true">
          <svg class="icon" viewBox="0 0 24 24"><rect x="4" y="4" width="16" height="16" rx="3" /><path d="M9 4v7l3 3 3-3V4m-3 10v6" /></svg>
        </span>
        <span class="flow-copy">
          <span class="flow-caption">监听入口</span>
          <span :key="entry" class="flow-endpoint flow-value">{{ entry }}</span>
        </span>
      </li>
      <li class="flow-step flow-target">
        <span class="flow-connector" aria-hidden="true">
          <span v-if="filled" :key="pulseKey" class="flow-packet"></span>
        </span>
        <span class="flow-symbol" aria-hidden="true">
          <svg class="icon" viewBox="0 0 24 24"><rect x="4" y="4" width="16" height="7" rx="2" /><rect x="4" y="13" width="16" height="7" rx="2" /><path d="M8 7.5h1m-1 9h1" /></svg>
        </span>
        <span class="flow-copy">
          <span class="flow-caption">转发目标</span>
          <span :key="target" class="flow-endpoint flow-value">{{ target }}</span>
        </span>
      </li>
    </ol>
  </section>
</template>
