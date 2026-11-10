<script setup lang="ts">
import { computed, onMounted, onUnmounted, ref } from 'vue'
import { formatEndpoint, listenAddressScope, request, type Endpoints, type Protocol, type Rule } from './api'
import AddressCombobox from './components/AddressCombobox.vue'
import RuleFlow from './components/RuleFlow.vue'
import ToastRegion from './components/ToastRegion.vue'
import { followTheme } from './theme'

const rules = ref<Rule[]>([])
const view = ref<'list' | 'form'>('list')
const editing = ref<Rule | null>(null)
const draft = ref(blankDraft())
const loading = ref(true)
const refreshing = ref(false)
const busy = ref(false)
const listError = ref('')
const deleteRule = ref<Rule | null>(null)
const dialog = ref<HTMLDialogElement>()
const toast = ref<InstanceType<typeof ToastRegion>>()
const viewHeading = ref<HTMLHeadingElement>()
let timer: number
let refreshVersion = 0
let seenRuleErrors = new Map<string, string>()

const statusLabels = { listening: '监听中', disabled: '已停用', failed: '启动失败' }
const protocolChoices = ['tcp', 'udp'] as const
const exposureNotice = computed(() => {
  const scope = listenAddressScope(draft.value.listenAddress)
  if (scope === 'all') return '将监听所有网卡，入口可能从公网访问。请确认目标服务已启用鉴权。'
  if (scope === 'public') return '此入口可能从公网访问。请确认目标服务已启用鉴权。'
  return ''
})

function blankDraft() {
  return { listenAddress: '*', listenPort: '', targetAddress: '127.0.0.1', targetPort: '', protocol: 'tcp' as Protocol, remark: '' }
}

function message(error: unknown) {
  return error instanceof Error ? error.message : '操作失败，请重试'
}

function showRuleError(rule: Rule) {
  const protocol = (rule.protocol ?? 'tcp').toUpperCase()
  toast.value?.show('error', protocol + ' ' + formatEndpoint(rule.listenAddress, rule.listenPort), rule.listenError || rule.targetError)
}

async function refresh(manual = false) {
  if (manual) refreshing.value = true
  const version = ++refreshVersion
  try {
    const result = await request<Rule[]>()
    if (version !== refreshVersion) return
    rules.value = result
    listError.value = ''
    const errors = new Map<string, string>()
    for (const rule of result) {
      const problem = rule.listenError || rule.targetError
      if (!problem) continue
      errors.set(rule.id, problem)
      if (seenRuleErrors.get(rule.id) !== problem) showRuleError(rule)
    }
    seenRuleErrors = errors
  } catch (error) {
    if (version !== refreshVersion) return
    const problem = message(error)
    if (manual || listError.value !== problem) toast.value?.show('error', '无法读取规则', problem)
    listError.value = problem
  } finally {
    if (version === refreshVersion) {
      loading.value = false
      refreshing.value = false
    }
  }
}

onMounted(() => {
  followTheme()
  void refresh()
  timer = window.setInterval(() => {
    if (!busy.value && !refreshing.value && view.value === 'list' && !document.hidden) void refresh()
  }, 4000)
})

onUnmounted(() => window.clearInterval(timer))

function showList() {
  view.value = 'list'
  void refresh()
}

function showForm(rule: Rule | null = null) {
  editing.value = rule
  draft.value = rule ? {
    listenAddress: rule.listenAddress,
    listenPort: String(rule.listenPort),
    targetAddress: rule.targetAddress,
    targetPort: String(rule.targetPort),
    protocol: rule.protocol ?? 'tcp',
    remark: rule.remark ?? '',
  } : blankDraft()
  view.value = 'form'
}

async function save() {
  if (busy.value) return
  const id = editing.value?.id
  const input: Endpoints = {
    listenAddress: draft.value.listenAddress.trim(),
    listenPort: Number(draft.value.listenPort),
    targetAddress: draft.value.targetAddress.trim(),
    targetPort: Number(draft.value.targetPort),
    protocol: draft.value.protocol,
    remark: draft.value.remark.trim(),
  }
  if (!input.listenAddress || !input.targetAddress) {
    toast.value?.show('error', '请填写地址', '入口地址和服务地址都需要填写。')
    return
  }
  if ([draft.value.listenPort, draft.value.targetPort].some(port => !/^\d{1,5}$/.test(port) || Number(port) < 1 || Number(port) > 65535)) {
    toast.value?.show('error', '端口格式有误', '入口端口和服务端口必须是 1–65535 的整数。')
    return
  }
  busy.value = true
  try {
    await request<Rule>(id ? '/' + id : '', id ? 'PUT' : 'POST', input)
    toast.value?.show('success', id ? '已保存规则' : '规则已开始监听')
    showList()
  } catch (error) {
    toast.value?.show('error', '无法保存规则', message(error))
  } finally {
    busy.value = false
  }
}

async function changeRule(action: () => Promise<unknown>, success: string) {
  if (busy.value) return
  busy.value = true
  try {
    await action()
    toast.value?.show('success', success)
    await refresh()
  } catch (error) {
    toast.value?.show('error', '操作失败', message(error))
  } finally {
    busy.value = false
  }
}

function toggle(rule: Rule, enabled: boolean) {
  void changeRule(
    () => request('/' + rule.id + '/enabled', 'PUT', { enabled }),
    enabled ? '规则已开始监听' : '规则已停用',
  )
}

function confirmDelete(rule: Rule) {
  deleteRule.value = rule
  dialog.value?.showModal()
}

function remove() {
  const rule = deleteRule.value
  if (!rule) return
  dialog.value?.close()
  void changeRule(() => request('/' + rule.id, 'DELETE'), '规则已删除')
}
</script>

<template>
  <div class="shell">
    <aside class="sidebar">
      <nav aria-label="应用菜单" class="nav">
        <button class="nav-item" type="button" :aria-current="view === 'list' ? 'page' : undefined"
          :disabled="busy" @click="showList">
          <svg viewBox="0 0 24 24" fill="none" aria-hidden="true"><path d="M4 6h15m-4-3 4 3-4 3M20 18H5m4-3-4 3 4 3" /></svg>
          转发规则
        </button>
        <button class="nav-item" type="button" :aria-current="view === 'form' && !editing ? 'page' : undefined"
          :disabled="busy" @click="showForm()">
          <svg viewBox="0 0 24 24" fill="none" aria-hidden="true"><path d="M12 5v14M5 12h14" /></svg>
          新建规则
        </button>
      </nav>
    </aside>

    <main class="panel">
      <Transition :name="view === 'form' ? 'view-forward' : 'view-back'" mode="out-in"
        @after-enter="viewHeading?.focus({ preventScroll: true })">
      <section v-if="view === 'list'" key="list" class="list-page" aria-labelledby="rules-title">
        <header class="page-header">
          <h1 id="rules-title" ref="viewHeading" tabindex="-1">转发规则</h1>
          <div class="toolbar">
            <button class="button" type="button" :disabled="busy || loading || refreshing" @click="refresh(true)">
              <svg class="icon" :class="{ spinner: refreshing }" viewBox="0 0 24 24" aria-hidden="true">
                <path v-if="refreshing" d="M20 12a8 8 0 1 1-8-8" />
                <path v-else d="M20 4v5h-5M4 20v-5h5M4.8 9a8 8 0 0 1 12.9-2.7L20 9M4 15l2.3 2.7A8 8 0 0 0 19.2 15" />
              </svg>
              {{ refreshing ? '刷新中…' : '刷新' }}
            </button>
            <button class="button primary" type="button" :disabled="busy" @click="showForm()">
              <svg class="icon" viewBox="0 0 24 24" aria-hidden="true"><path d="M12 5v14M5 12h14" /></svg>
              新建规则
            </button>
          </div>
        </header>

        <div class="list-body" :aria-busy="loading || refreshing">
          <div v-if="loading" class="skeleton-list">
            <span class="sr-only" role="status">正在读取转发规则</span>
            <div v-for="item in 3" :key="item" class="rule-skeleton" aria-hidden="true">
              <span class="skeleton-icon"></span>
              <span class="skeleton-lines"><i></i><i></i></span>
            </div>
          </div>
          <div v-else-if="rules.length === 0" class="empty">
            <svg viewBox="0 0 24 24" fill="none" aria-hidden="true"><path d="M3 8h17m-4-4 4 4-4 4m5 4H4m4-4-4 4 4 4" /></svg>
            <h2>{{ listError ? '暂时无法读取规则' : '还没有转发规则' }}</h2>
            <button v-if="listError" type="button" class="button mt-2" @click="refresh(true)">重试</button>
            <p v-else class="muted mt-1">新建规则，填写 NAS 入口和要转发到的服务。</p>
          </div>
          <TransitionGroup v-else tag="ul" name="rules" class="grid gap-3" aria-label="规则列表">
            <li v-for="rule in rules" :key="rule.id" class="rule-card" :data-status="rule.status">
              <div class="rule-main">
                <div class="rule-symbol" aria-hidden="true">
                  <svg class="icon" viewBox="0 0 24 24"><path d="M4 8h16m-4-4 4 4-4 4m4 4H4m4-4-4 4 4 4" /></svg>
                  <span class="status-dot"></span>
                </div>
                <div class="rule-details">
                  <div class="rule-route">
                    <div class="endpoint">
                      <span class="muted text-xs">NAS 入口</span>
                      <span>{{ formatEndpoint(rule.listenAddress, rule.listenPort) }}</span>
                    </div>
                    <svg class="icon route-arrow" viewBox="0 0 24 24" aria-hidden="true">
                      <path d="M5 12h14m-6-6 6 6-6 6" />
                    </svg>
                    <div class="endpoint">
                      <span class="muted text-xs">转发到</span>
                      <span>{{ formatEndpoint(rule.targetAddress, rule.targetPort) }}</span>
                    </div>
                  </div>
                  <p v-if="rule.remark" class="rule-remark">{{ rule.remark }}</p>
                </div>
              </div>
              <div class="rule-actions">
                <span class="protocol-badge">{{ (rule.protocol ?? 'tcp').toUpperCase() }}</span>
                <span class="status">{{ statusLabels[rule.status] }}</span>
                <button v-if="rule.listenError || rule.targetError" class="icon-button danger" type="button"
                  title="查看错误" aria-label="查看错误" @click="showRuleError(rule)">
                  <svg class="icon" viewBox="0 0 24 24" aria-hidden="true"><circle cx="12" cy="12" r="9" /><path d="M12 7.5v5.2m0 3.3h.01" /></svg>
                </button>
                <button v-if="rule.status === 'failed'" class="icon-button" type="button" title="重试" aria-label="重试"
                  :disabled="busy" @click="toggle(rule, true)">
                  <svg class="icon" viewBox="0 0 24 24" aria-hidden="true"><path d="M20 5v6h-6m6 0a8 8 0 1 0-.7 5" /></svg>
                </button>
                <button class="rule-toggle" type="button" role="switch" aria-label="启用规则"
                  :aria-checked="rule.enabled" :title="rule.enabled ? '停用规则' : '启用规则'"
                  :disabled="busy" @click="toggle(rule, !rule.enabled)">
                  <span class="rule-toggle-track" aria-hidden="true"><span class="rule-toggle-thumb"></span></span>
                </button>
                <button v-if="!rule.enabled" class="icon-button" type="button" title="编辑" aria-label="编辑"
                  :disabled="busy" @click="showForm(rule)">
                  <svg class="icon" viewBox="0 0 24 24" aria-hidden="true"><path d="m14 6 4 4m-8 8-5 1 1-5L17 3l4 4z" /></svg>
                </button>
                <button class="icon-button danger" type="button" title="删除" aria-label="删除"
                  :disabled="busy" @click="confirmDelete(rule)">
                  <svg class="icon" viewBox="0 0 24 24" aria-hidden="true"><path d="M4 7h16M9 7V4h6v3M6 7l1 13h10l1-13m-8 4v5m4-5v5" /></svg>
                </button>
              </div>
            </li>
          </TransitionGroup>
        </div>
        <footer v-if="!loading && !listError" class="list-footer">共 {{ rules.length }} 条规则</footer>
      </section>

      <section v-else key="form" class="form-page" aria-labelledby="form-title">
        <form class="rule-form" novalidate @submit.prevent="save">
          <header class="form-head">
            <h1 id="form-title" ref="viewHeading" tabindex="-1">{{ editing ? '编辑规则' : '新建规则' }}</h1>
            <fieldset class="protocol-row" :disabled="busy">
              <legend class="sr-only">转发协议</legend>
              <div class="protocol-picker">
                <label v-for="protocol in protocolChoices" :key="protocol">
                  <input v-model="draft.protocol" type="radio" name="protocol" :value="protocol" />
                  <span>{{ protocol.toUpperCase() }}</span>
                </label>
              </div>
            </fieldset>
          </header>
          <div class="rule-settings">
            <fieldset class="field-group" :disabled="busy">
              <legend class="sr-only">NAS 入口</legend>
              <div class="field-role" aria-hidden="true">
                <strong>NAS 入口</strong>
                <span>接收流量</span>
              </div>
              <div class="field-pair">
                <AddressCombobox id="listen-address" v-model="draft.listenAddress"
                  label="入口地址" listen described-by="listen-help" />
                <label for="listen-port">
                  入口端口
                  <input id="listen-port" v-model="draft.listenPort" inputmode="numeric" pattern="[0-9]{1,5}"
                    required maxlength="5" placeholder="8080" />
                </label>
              </div>
              <p v-if="exposureNotice" id="listen-help" class="exposure-note" role="status">
                <svg class="icon" viewBox="0 0 24 24" aria-hidden="true"><circle cx="12" cy="12" r="9" /><path d="M12 7.5v5.2m0 3.3h.01" /></svg>
                <span>{{ exposureNotice }}</span>
              </p>
              <p v-else id="listen-help" class="sr-only">选择监听范围，或输入 NAS 的具体 IP。</p>
            </fieldset>
            <fieldset class="field-group" :disabled="busy">
              <legend class="sr-only">目标服务</legend>
              <div class="field-role" aria-hidden="true">
                <strong>目标服务</strong>
                <span>接收转发</span>
              </div>
              <div class="field-pair">
                <AddressCombobox id="target-address" v-model="draft.targetAddress" label="服务地址" />
                <label for="target-port">
                  服务端口
                  <input id="target-port" v-model="draft.targetPort" inputmode="numeric" pattern="[0-9]{1,5}"
                    required maxlength="5" placeholder="80" />
                </label>
              </div>
            </fieldset>
            <div class="field-group">
              <label class="field-role" for="rule-remark">
                <strong>备注</strong>
                <span>可选</span>
              </label>
              <input id="rule-remark" v-model="draft.remark" :disabled="busy" maxlength="120" />
            </div>
          </div>
          <RuleFlow :protocol="draft.protocol" :listen-address="draft.listenAddress" :listen-port="draft.listenPort"
            :target-address="draft.targetAddress" :target-port="draft.targetPort" />
          <div class="form-actions">
            <button class="button" type="button" :disabled="busy" @click="showList">取消</button>
            <button class="button primary" type="submit" :disabled="busy">
              <svg v-if="busy" class="icon spinner" viewBox="0 0 24 24" aria-hidden="true"><path d="M20 12a8 8 0 1 1-8-8" /></svg>
              {{ busy ? '正在保存…' : editing ? '保存规则' : '保存并启用' }}
            </button>
          </div>
        </form>
      </section>
      </Transition>
    </main>

    <ToastRegion ref="toast" />

    <dialog ref="dialog" aria-labelledby="delete-title" @close="deleteRule = null">
      <h2 id="delete-title" class="text-base font-semibold">删除这条规则？</h2>
      <p v-if="deleteRule" class="muted mt-2 break-all text-sm">
        {{ formatEndpoint(deleteRule.listenAddress, deleteRule.listenPort) }}
        → {{ formatEndpoint(deleteRule.targetAddress, deleteRule.targetPort) }}
      </p>
      <p v-if="deleteRule?.enabled" class="muted mt-1 text-sm">删除会关闭正在通过这条规则转发的连接。</p>
      <div class="mt-5 flex justify-end gap-2">
        <button type="button" class="button" autofocus @click="dialog?.close()">取消</button>
        <button type="button" class="button danger-solid" @click="remove">删除</button>
      </div>
    </dialog>
  </div>
</template>
