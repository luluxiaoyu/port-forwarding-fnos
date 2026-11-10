export type Protocol = 'tcp' | 'udp'

export interface Endpoints {
  listenAddress: string
  listenPort: number
  targetAddress: string
  targetPort: number
  protocol: Protocol
  remark?: string
}

export interface Rule extends Endpoints {
  id: string
  enabled: boolean
  status: 'listening' | 'disabled' | 'failed'
  listenError?: string
  targetError?: string
}

export async function request<T>(path = '', method = 'GET', body?: object): Promise<T> {
  const headers = new Headers({ Accept: 'application/json' })
  if (method !== 'GET') headers.set('X-Port-Forwarding-Request', '1')
  if (body) headers.set('Content-Type', 'application/json')
  const response = await fetch(import.meta.env.BASE_URL + 'api/rules' + path, {
    method,
    headers,
    credentials: 'same-origin',
    body: body ? JSON.stringify(body) : undefined,
  })
  if (response.status === 204) return undefined as T
  const data = await response.json().catch(() => null)
  if (!response.ok || data === null) {
    throw new Error(data?.message ?? (response.status === 403
      ? '需要飞牛管理员权限'
      : '无法访问转发服务，请检查应用是否正在运行'))
  }
  return data
}

export function formatEndpoint(host: string, port: number | string) {
  if (host === '*') return '全部 IPv4 / IPv6 · ' + port
  return (host.includes(':') ? '[' + host + ']' : host) + ':' + port
}
