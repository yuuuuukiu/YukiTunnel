/**
 * Thin typed wrappers around the Rust commands. Every call degrades to a
 * descriptive error when the UI is opened in a plain browser (`pnpm dev`),
 * which keeps the web build usable for design review.
 */

export interface CheckItem {
  id: string;
  label: string;
  ok: boolean;
  detail: string;
  fatal: boolean;
}

export interface EnvReport {
  os: string;
  arch: string;
  binary_found: boolean;
  binary_path: string | null;
  binary_version: string | null;
  data_dir: string;
  data_dir_writable: boolean;
  api_port_free: boolean;
  control_port_free: boolean;
  docker_available: boolean;
  existing_deployment: boolean;
  checks: CheckItem[];
  ready: boolean;
}

export interface DeployRequest {
  data_dir: string;
  api_port: number;
  control_port: number;
  public_host: string;
  admin_user: string;
  admin_password: string;
  domain_binding: boolean;
  domain_suffix: string;
  port_min: number;
  port_max: number;
  tls_mode: 'self-signed' | 'existing';
  tls_cert?: string;
  tls_key?: string;
}

export interface DeployResult {
  ok: boolean;
  webui_url: string;
  control_addr: string;
  fingerprint: string | null;
  admin_user: string;
  generated_password: string | null;
  data_dir: string;
  log_file: string | null;
}

export interface ServerStatus {
  running: boolean;
  healthy: boolean;
  data_dir: string | null;
  webui_url: string | null;
  last_error: string | null;
}

export interface AgentStatus {
  running: boolean;
  pid: number | null;
  last_error: string | null;
  log_path: string | null;
}

export interface Progress {
  step: string;
  status: 'running' | 'done' | 'error';
  message: string;
  detail?: string | null;
  percent: number;
}

export interface PathsInfo {
  config_dir: string;
  data_dir: string;
  server_binary: string | null;
  client_binary: string | null;
}

export const isDesktop = '__TAURI_INTERNALS__' in window;

async function invoke<T>(cmd: string, args?: Record<string, unknown>): Promise<T> {
  if (!isDesktop) {
    throw new Error('该功能需要在 YukiTunnel 桌面客户端中使用');
  }
  const { invoke: tauriInvoke } = await import('@tauri-apps/api/core');
  return tauriInvoke<T>(cmd, args);
}

export async function listen<T>(event: string, handler: (payload: T) => void): Promise<() => void> {
  if (!isDesktop) return () => {};
  const { listen: tauriListen } = await import('@tauri-apps/api/event');
  const unlisten = await tauriListen<T>(event, (e) => handler(e.payload));
  return unlisten;
}

export const desktop = {
  envCheck: (opts: { dataDir?: string; apiPort?: number; controlPort?: number } = {}) =>
    invoke<EnvReport>('env_check', { dataDir: opts.dataDir, apiPort: opts.apiPort, controlPort: opts.controlPort }),

  deployServer: (req: DeployRequest) => invoke<DeployResult>('deploy_server', { req }),

  serverStatus: () => invoke<ServerStatus>('server_status'),

  stopServer: () => invoke<boolean>('stop_server'),

  serverLogs: (lines = 200) => invoke<string>('server_logs', { lines }),

  installNotes: () => invoke<{ platform: string; firewall_hint: string; docker_alternative: string }>('install_notes'),

  agentStart: (req: {
    serverUrl: string;
    token?: string;
    username?: string;
    keyPath?: string;
    insecure?: boolean;
    tunnelIds?: string[];
    autoReconnect?: boolean;
  }) =>
    invoke<AgentStatus>('agent_start', {
      req: {
        server_url: req.serverUrl,
        token: req.token,
        username: req.username,
        key_path: req.keyPath,
        insecure: req.insecure ?? false,
        tunnel_ids: req.tunnelIds ?? [],
        auto_reconnect: req.autoReconnect ?? true,
      },
    }),

  agentStop: () => invoke<AgentStatus>('agent_stop'),

  agentStatus: () => invoke<AgentStatus>('agent_status'),

  agentLogs: (lines = 200) => invoke<string>('agent_logs', { lines }),

  paths: () => invoke<PathsInfo>('paths_info'),

  pickDirectory: async (): Promise<string | null> => {
    if (!isDesktop) return null;
    const { open } = await import('@tauri-apps/plugin-dialog');
    const res = await open({ directory: true, multiple: false, title: '选择数据目录' });
    return typeof res === 'string' ? res : null;
  },

  readPrivateKey: (path: string) => invoke<string>('read_private_key', { path }),

  pickFile: async (filters?: { name: string; extensions: string[] }[]): Promise<string | null> => {
    if (!isDesktop) return null;
    const { open } = await import('@tauri-apps/plugin-dialog');
    const res = await open({ directory: false, multiple: false, filters, title: '选择文件' });
    return typeof res === 'string' ? res : null;
  },

  openExternal: async (url: string) => {
    if (!isDesktop) {
      window.open(url, '_blank', 'noopener');
      return;
    }
    const { openUrl } = await import('@tauri-apps/plugin-opener');
    await openUrl(url);
  },
};
