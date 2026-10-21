import type {
  AdminNode, AuditEntry, LoginResult, Overview, ServerNode, Settings, Tunnel, User, UserKey,
} from './types';

/** ApiError carries the server's stable code plus a human message. */
export class ApiError extends Error {
  code: string;
  status: number;
  details?: Record<string, unknown>;
  constructor(status: number, code: string, message: string, details?: Record<string, unknown>) {
    super(message);
    this.status = status;
    this.code = code;
    this.details = details;
  }
}

let baseUrl = '/api/v1';
let token: string | null = null;
let onUnauthorized: (() => void) | null = null;

/**
 * Transport lets a host (the Tauri desktop app) replace fetch. The desktop
 * shell proxies requests through Rust so self-signed server certificates work
 * without weakening the webview's TLS policy.
 */
export interface RawResponse {
  status: number;
  body: string;
}

export type Transport = (req: {
  method: string;
  url: string;
  token: string | null;
  body?: string;
}) => Promise<RawResponse>;

let transport: Transport | null = null;

export function setTransport(fn: Transport | null) {
  transport = fn;
}

export function getBaseUrl() {
  return baseUrl;
}

export function configureApi(opts: { baseUrl?: string; token?: string | null }) {
  if (opts.baseUrl !== undefined) baseUrl = opts.baseUrl.replace(/\/$/, '');
  if (opts.token !== undefined) token = opts.token;
}

export function setUnauthorizedHandler(fn: () => void) {
  onUnauthorized = fn;
}

export function currentToken() {
  return token;
}

async function request<T>(method: string, path: string, body?: unknown): Promise<T> {
  const url = baseUrl + path;
  const payload = body !== undefined ? JSON.stringify(body) : undefined;
  let status: number;
  let text: string;
  try {
    if (transport) {
      const res = await transport({ method, url, token, body: payload });
      status = res.status;
      text = res.body;
    } else {
      const resp = await fetch(url, {
        method,
        headers: {
          Accept: 'application/json',
          ...(payload !== undefined ? { 'Content-Type': 'application/json' } : {}),
          ...(token ? { Authorization: `Bearer ${token}` } : {}),
        },
        body: payload,
      });
      status = resp.status;
      text = await resp.text();
    }
  } catch (err) {
    const detail = err instanceof Error && err.message ? `（${err.message}）` : '';
    throw new ApiError(0, 'NETWORK', '无法连接服务端，请检查地址与网络' + detail);
  }
  const data = text ? safeParse(text) : null;
  if (status < 200 || status >= 300) {
    const err = (data as { error?: { code: string; message: string; details?: Record<string, unknown> } } | null)?.error;
    if (status === 401 && onUnauthorized) onUnauthorized();
    throw new ApiError(status, err?.code ?? 'HTTP_' + status, err?.message ?? `请求失败（${status}）`, err?.details);
  }
  return data as T;
}

function safeParse(text: string): unknown {
  try {
    return JSON.parse(text);
  } catch {
    return null;
  }
}

export const api = {
  /** request is exposed for feature modules (e.g. cloud profiles). */
  request,

  login: (username: string, password: string) =>
    request<LoginResult>('POST', '/auth/login', { username, password }),

  keyChallenge: (username: string) =>
    request<{ nonce: string; expires_in: number }>('POST', '/auth/key/challenge', { username }),

  keyLogin: (username: string, nonce: string, signature: string) =>
    request<LoginResult>('POST', '/auth/key/login', { username, nonce, signature }),

  logout: () => request<{ ok: boolean }>('POST', '/auth/logout'),

  me: () => request<{ user: User; domain_binding: boolean; domain_suffix: string }>('GET', '/me'),

  changePassword: (current_password: string, new_password: string) =>
    request<{ ok: boolean }>('POST', '/me/password', { current_password, new_password }),

  myKeys: () => request<{ keys: UserKey[] }>('GET', '/me/keys'),
  addMyKey: (name: string, public_key: string) =>
    request<{ key: UserKey }>('POST', '/me/keys', { name, public_key }),
  deleteMyKey: (id: string) => request<{ ok: boolean }>('DELETE', `/me/keys/${id}`),

  servers: () =>
    request<{ servers: ServerNode[]; domain_binding: boolean; domain_suffix: string }>('GET', '/servers'),

  tunnels: () => request<{ tunnels: Tunnel[] }>('GET', '/tunnels'),
  createTunnel: (body: {
    name?: string;
    node_id: string;
    proto: string;
    local_ip: string;
    local_port: number;
    public_port?: number;
    use_domain?: boolean;
  }) => request<{ tunnel: Tunnel }>('POST', '/tunnels', body),
  updateTunnel: (id: string, body: Record<string, unknown>) =>
    request<{ tunnel: Tunnel }>('PATCH', `/tunnels/${id}`, body),
  deleteTunnel: (id: string) => request<{ ok: boolean }>('DELETE', `/tunnels/${id}`),
  startTunnel: (id: string) => request<{ tunnel: Tunnel; address: string }>('POST', `/tunnels/${id}/start`),
  stopTunnel: (id: string) => request<{ tunnel: Tunnel }>('POST', `/tunnels/${id}/stop`),
  tunnelStats: (id: string) =>
    request<{ active: boolean; rx_bytes: number; tx_bytes: number; connections: number; active_connections: number }>(
      'GET',
      `/tunnels/${id}/stats`,
    ),

  overview: () => request<Overview>('GET', '/admin/overview'),
  users: () => request<{ users: User[] }>('GET', '/admin/users'),
  createUser: (body: {
    username: string;
    display_name?: string;
    password?: string;
    role?: string;
    public_key?: string;
    key_name?: string;
  }) => request<{ user: User }>('POST', '/admin/users', body),
  updateUser: (id: string, body: Record<string, unknown>) =>
    request<{ user: User }>('PATCH', `/admin/users/${id}`, body),
  deleteUser: (id: string) => request<{ ok: boolean }>('DELETE', `/admin/users/${id}`),
  userKeys: (id: string) => request<{ keys: UserKey[] }>('GET', `/admin/users/${id}/keys`),
  addUserKey: (id: string, name: string, public_key: string) =>
    request<{ key: UserKey }>('POST', `/admin/users/${id}/keys`, { name, public_key }),
  deleteUserKey: (id: string, keyId: string) =>
    request<{ ok: boolean }>('DELETE', `/admin/users/${id}/keys/${keyId}`),

  nodes: () => request<{ nodes: AdminNode[] }>('GET', '/admin/nodes'),
  createNode: (body: Record<string, unknown>) => request<{ node: AdminNode }>('POST', '/admin/nodes', body),
  updateNode: (id: string, body: Record<string, unknown>) =>
    request<{ node: AdminNode }>('PATCH', `/admin/nodes/${id}`, body),
  deleteNode: (id: string) => request<{ ok: boolean }>('DELETE', `/admin/nodes/${id}`),
  checkNode: (id: string) =>
    request<{ node_id: string; status: string; latency_ms: number }>('POST', `/admin/nodes/${id}/check`),

  adminTunnels: (userId?: string) =>
    request<{ tunnels: Tunnel[] }>('GET', '/admin/tunnels' + (userId ? `?user_id=${userId}` : '')),
  adminDeleteTunnel: (id: string) => request<{ ok: boolean }>('DELETE', `/admin/tunnels/${id}`),
  adminToggleTunnel: (id: string, enabled: boolean) =>
    request<{ tunnel: Tunnel }>('POST', `/admin/tunnels/${id}/${enabled ? 'enable' : 'disable'}`),

  settings: () => request<{ settings: Settings }>('GET', '/admin/settings'),
  updateSettings: (body: Record<string, unknown>) =>
    request<{ settings: Settings }>('PUT', '/admin/settings', body),

  audit: (limit = 100, offset = 0) =>
    request<{ entries: AuditEntry[]; limit: number; offset: number }>('GET', `/admin/audit?limit=${limit}&offset=${offset}`),
};
