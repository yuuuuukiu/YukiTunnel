export type Role = 'admin' | 'user';

export interface User {
  id: string;
  username: string;
  display_name: string;
  role: Role;
  disabled: boolean;
  created_at: string;
  last_login_at: string;
  key_count?: number;
  tunnel_count?: number;
}

export interface LoginResult {
  token: string;
  expires_at: string;
  user: User;
}

export interface ServerNode {
  id: string;
  name: string;
  region: string;
  public_host: string;
  status: string;
  online: boolean;
  latency_ms: number;
  domain?: string;
  port_range: [number, number];
  max_tunnels: number;
  tunnel_count: number;
}

export type Proto = 'tcp' | 'udp' | 'http' | 'https';

export interface Tunnel {
  id: string;
  user_id: string;
  node_id: string;
  node_name: string;
  name: string;
  proto: Proto;
  local_ip: string;
  local_port: number;
  public_port: number;
  domain: string;
  address: string;
  public_host: string;
  active: boolean;
  enabled: boolean;
  desired_running: boolean;
  rx_bytes: number;
  tx_bytes: number;
  connections: number;
  active_connections: number;
  created_at: string;
  last_started_at: string;
}

export interface AdminNode {
  id: string;
  name: string;
  region: string;
  public_host: string;
  control_host: string;
  control_port: number;
  enabled: boolean;
  status: string;
  latency_ms: number;
  max_tunnels: number;
  last_seen_at: string;
}

export interface Settings {
  domain_binding: boolean;
  domain_suffix: string;
  public_host: string;
  port_min: number;
  port_max: number;
  port_allowlist: number[] | null;
  api_addr: string;
  control_addr: string;
  version: string;
}

export interface AuditEntry {
  id: string;
  actor_name: string;
  action: string;
  target_type: string;
  target_id: string;
  detail: string;
  ip: string;
  created_at: string;
}

export interface Overview {
  counts: {
    users: number;
    nodes: number;
    nodes_online: number;
    tunnels: number;
    tunnels_active: number;
  };
  settings: Settings;
  recent_audit: AuditEntry[];
  version: string;
}

export interface UserKey {
  id: string;
  user_id: string;
  name: string;
  fingerprint: string;
  public_key: string;
  created_at: string;
}

export interface ProfileIndexEntry {
  id: string;
  vendor: string;
  name: string;
  version: string;
  path: string;
  sha256: string;
  docs?: string[];
  min_engine?: string;
}

export interface ProfileIndex {
  source: string;
  generated_at: string;
  key_id: string;
  profiles: ProfileIndexEntry[];
  verified: boolean;
}

export interface CloudProfile {
  id: string;
  vendor: string;
  name: string;
  version: string;
  source: string;
  sha256: string;
  enabled: boolean;
  installed_at: string;
  updated_at: string;
}

export interface ProfileCredential {
  id: string;
  profile_id: string;
  label: string;
  created_at: string;
  updated_at: string;
}

export interface ProfileRun {
  id: string;
  profile_id: string;
  profile_sha256: string;
  action: string;
  node_id: string;
  port_min: number;
  port_max: number;
  protocol: string;
  status: string;
  message: string;
  duration_ms: number;
  actor_name: string;
  created_at: string;
}

export interface ProfileSettings {
  mirrors: string[];
  proxy: string;
  source_cidr: string;
  trusted_keys: string[];
}
