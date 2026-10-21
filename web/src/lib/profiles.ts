import { api } from './api';
import type { CloudProfile, ProfileIndex, ProfileRun, ProfileSettings, ProfileCredential } from './types';

export const profilesApi = {
  index: () => api.request<ProfileIndex>('GET', '/admin/profiles/index'),
  installed: () => api.request<{ profiles: CloudProfile[] }>('GET', '/admin/profiles'),
  install: (body: { id?: string; document?: string; sha256?: string }) =>
    api.request<{ profile: CloudProfile }>('POST', '/admin/profiles/install', body),
  get: (id: string) => api.request<{ profile: CloudProfile; document: string }>('GET', `/admin/profiles/${id}`),
  toggle: (id: string, enabled: boolean) =>
    api.request<{ ok: boolean }>('POST', `/admin/profiles/${id}/${enabled ? 'enable' : 'disable'}`),
  remove: (id: string) => api.request<{ ok: boolean }>('DELETE', `/admin/profiles/${id}`),
  credentials: (id: string) =>
    api.request<{ credentials: ProfileCredential[] }>('GET', `/admin/profiles/${id}/credentials`),
  setCredential: (id: string, label: string, fields: Record<string, string>) =>
    api.request<{ ok: boolean }>('PUT', `/admin/profiles/${id}/credentials`, { label, fields }),
  deleteCredential: (id: string, credId: string) =>
    api.request<{ ok: boolean }>('DELETE', `/admin/profiles/${id}/credentials/${credId}`),
  run: (id: string, body: {
    action: 'ensure_port_allowed' | 'revoke_port_allowed';
    node_id?: string;
    port_min: number;
    port_max?: number;
    protocol?: string;
    credential_label?: string;
    params?: Record<string, unknown>;
    dry_run?: boolean;
    acknowledge_permissions: boolean;
  }) => api.request<{ result: unknown; run: ProfileRun }>('POST', `/admin/profiles/${id}/run`, body),
  runs: (limit = 100) => api.request<{ runs: ProfileRun[] }>('GET', `/admin/profile-runs?limit=${limit}`),
  settings: () => api.request<ProfileSettings>('GET', '/admin/profile-settings'),
  updateSettings: (body: { mirrors?: string; proxy?: string; source_cidr?: string }) =>
    api.request<ProfileSettings>('PUT', '/admin/profile-settings', body),
};
