import { useMemo, useState } from 'react';
import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query';
import {
  CheckCircle2, Cloud, Download, KeyRound, Loader2, Play, RefreshCw, ShieldCheck, Trash2, TriangleAlert,
} from 'lucide-react';
import { Badge, Button, Card, CardHeader, Field, Modal, Spinner, Switch } from '../../components/ui';
import { profilesApi } from '../../lib/profiles';
import { api } from '../../lib/api';
import type { CloudProfile, ProfileIndexEntry, ProfileSettings } from '../../lib/types';
import { useToast } from '../../state/toast';
import { formatTime } from '../../lib/format';

const VENDOR_LABEL: Record<string, string> = {
  aliyun: '阿里云',
  tencent: '腾讯云',
  aws: 'AWS',
  huawei: '华为云',
  azure: 'Azure',
  gcp: 'Google Cloud',
};

export default function ProfilesPage() {
  const qc = useQueryClient();
  const toast = useToast();
  const installed = useQuery({ queryKey: ['profiles'], queryFn: profilesApi.installed });
  const index = useQuery({ queryKey: ['profile-index'], queryFn: profilesApi.index, retry: 1 });
  const runs = useQuery({ queryKey: ['profile-runs'], queryFn: () => profilesApi.runs(20) });
  const settings = useQuery({ queryKey: ['profile-settings'], queryFn: profilesApi.settings });

  const [installOpen, setInstallOpen] = useState(false);
  const [detail, setDetail] = useState<CloudProfile | null>(null);
  const [runTarget, setRunTarget] = useState<CloudProfile | null>(null);
  const [settingsOpen, setSettingsOpen] = useState(false);

  const install = useMutation({
    mutationFn: (id: string) => profilesApi.install({ id }),
    onSuccess: () => {
      toast.success('profile 已安装并验签');
      void qc.invalidateQueries({ queryKey: ['profiles'] });
    },
    onError: (e) => toast.error('安装失败', e instanceof Error ? e.message : ''),
  });

  const toggle = useMutation({
    mutationFn: ({ id, enabled }: { id: string; enabled: boolean }) => profilesApi.toggle(id, enabled),
    onSuccess: () => void qc.invalidateQueries({ queryKey: ['profiles'] }),
    onError: (e) => toast.error('操作失败', e instanceof Error ? e.message : ''),
  });

  const remove = useMutation({
    mutationFn: (id: string) => profilesApi.remove(id),
    onSuccess: () => {
      toast.success('已移除 profile');
      void qc.invalidateQueries({ queryKey: ['profiles'] });
    },
  });

  const installedIDs = useMemo(
    () => new Set((installed.data?.profiles ?? []).map((p) => p.id)),
    [installed.data],
  );

  return (
    <>
      <div className="page-head">
        <div>
          <h1>云 Profile</h1>
          <p>声明式 YAML 描述如何在云厂商安全组放行端口。签名校验始终启用，无法关闭。</p>
        </div>
        <div className="row gap-8">
          <Button variant="subtle" onClick={() => setSettingsOpen(true)}>
            <ShieldCheck size={15} /> 镜像与代理
          </Button>
          <Button onClick={() => setInstallOpen(true)}>
            <Download size={15} /> 安装 profile
          </Button>
        </div>
      </div>

      <Card>
        <CardHeader
          title="签名源状态"
          subtitle="官方镜像 → jsDelivr → GitHub raw，逐个尝试；每个源都必须通过 ed25519 验签"
          actions={
            <Button size="sm" variant="ghost" onClick={() => void index.refetch()} disabled={index.isFetching}>
              <RefreshCw size={13} className={index.isFetching ? 'spinner-icon' : ''} /> 刷新索引
            </Button>
          }
        />
        {index.isLoading ? (
          <Spinner label="正在拉取并验签…" />
        ) : index.error ? (
          <div className="alert alert-bad">
            <TriangleAlert size={16} />
            <div>
              无法获取签名索引：{index.error instanceof Error ? index.error.message : '未知错误'}
              <div className="small" style={{ marginTop: 4 }}>
                镜像与代理可在右上角配置；离线时可使用本地缓存的索引（同样会验签）。
              </div>
            </div>
          </div>
        ) : (
          <div className="row gap-16 wrap small">
            <Badge tone="good"><CheckCircle2 size={12} /> 签名已验证</Badge>
            <span className="muted">来源 <span className="mono">{index.data?.source}</span></span>
            <span className="muted">密钥 <span className="mono">{index.data?.key_id}</span></span>
            <span className="faint">生成于 {formatTime(index.data?.generated_at ?? '')}</span>
            <span className="faint">共 {index.data?.profiles.length ?? 0} 个 profile</span>
          </div>
        )}
      </Card>

      <Card>
        <CardHeader title={`已安装（${installed.data?.profiles.length ?? 0}）`} subtitle="执行前会再次校验文档结构与哈希" />
        {installed.isLoading ? (
          <Spinner />
        ) : (installed.data?.profiles.length ?? 0) === 0 ? (
          <p className="small faint">尚未安装任何 profile。点击「安装 profile」从签名镜像获取。</p>
        ) : (
          <div className="table-wrap">
            <table>
              <thead>
                <tr>
                  <th>Profile</th>
                  <th>厂商</th>
                  <th>版本</th>
                  <th>SHA256</th>
                  <th>来源</th>
                  <th>启用</th>
                  <th className="right">操作</th>
                </tr>
              </thead>
              <tbody>
                {installed.data!.profiles.map((p) => (
                  <tr key={p.id}>
                    <td>
                      <strong>{p.name}</strong>
                      <div className="faint small mono">{p.id}</div>
                    </td>
                    <td><Badge tone="info">{VENDOR_LABEL[p.vendor] ?? p.vendor}</Badge></td>
                    <td className="mono small">{p.version}</td>
                    <td className="mono small">{p.sha256.slice(0, 12)}…</td>
                    <td className="small faint" style={{ maxWidth: 220, overflow: 'hidden', textOverflow: 'ellipsis' }}>{p.source}</td>
                    <td>
                      <Switch checked={p.enabled} label={`启用 ${p.id}`} onChange={(v) => toggle.mutate({ id: p.id, enabled: v })} />
                    </td>
                    <td className="right">
                      <div className="row gap-8" style={{ justifyContent: 'flex-end' }}>
                        <Button size="sm" variant="primary" onClick={() => setRunTarget(p)}>
                          <Play size={12} /> 放行端口
                        </Button>
                        <button className="icon-btn" title="查看 YAML" onClick={() => setDetail(p)}><Cloud size={15} /></button>
                        <button className="icon-btn" title="移除" onClick={() => remove.mutate(p.id)}><Trash2 size={15} /></button>
                      </div>
                    </td>
                  </tr>
                ))}
              </tbody>
            </table>
          </div>
        )}
      </Card>

      <Card>
        <CardHeader title="最近执行" subtitle="只记录 profile 版本与哈希，不记录凭证" actions={
          <Button size="sm" variant="ghost" onClick={() => void runs.refetch()}><RefreshCw size={13} /> 刷新</Button>
        } />
        {runs.isLoading ? <Spinner /> : (runs.data?.runs.length ?? 0) === 0 ? (
          <p className="small faint">暂无执行记录。</p>
        ) : (
          <div className="table-wrap">
            <table>
              <thead>
                <tr>
                  <th>时间</th>
                  <th>Profile</th>
                  <th>动作</th>
                  <th>端口</th>
                  <th>状态</th>
                  <th>耗时</th>
                  <th>结果</th>
                </tr>
              </thead>
              <tbody>
                {runs.data!.runs.map((r) => (
                  <tr key={r.id}>
                    <td className="small mono">{formatTime(r.created_at)}</td>
                    <td className="small mono">{r.profile_id}</td>
                    <td className="small">{r.action}</td>
                    <td className="mono small">{r.port_min}{r.port_max && r.port_max !== r.port_min ? `-${r.port_max}` : ''}</td>
                    <td>
                      {r.status === 'success' ? <Badge tone="good">成功</Badge>
                        : r.status === 'skipped' ? <Badge tone="neutral">已存在</Badge>
                          : <Badge tone="bad">失败</Badge>}
                    </td>
                    <td className="mono small">{r.duration_ms}ms</td>
                    <td className="small muted" style={{ maxWidth: 320, overflow: 'hidden', textOverflow: 'ellipsis' }}>{r.message}</td>
                  </tr>
                ))}
              </tbody>
            </table>
          </div>
        )}
      </Card>

      <InstallModal
        open={installOpen}
        onClose={() => setInstallOpen(false)}
        entries={index.data?.profiles ?? []}
        installed={installedIDs}
        onInstall={(id) => install.mutate(id)}
        busy={install.isPending}
      />

      <ProfileDetail profile={detail} onClose={() => setDetail(null)} />

      {runTarget && <RunModal profile={runTarget} onClose={() => setRunTarget(null)} />}

      {settingsOpen && settings.data && (
        <SettingsModal initial={settings.data} onClose={() => setSettingsOpen(false)} />
      )}
    </>
  );
}

function InstallModal({
  open, onClose, entries, installed, onInstall, busy,
}: {
  open: boolean;
  onClose: () => void;
  entries: ProfileIndexEntry[];
  installed: Set<string>;
  onInstall: (id: string) => void;
  busy: boolean;
}) {
  return (
    <Modal
      open={open}
      title="安装云 Profile"
      subtitle="仅展示已通过签名校验的索引条目"
      onClose={onClose}
      width={720}
      footer={<Button variant="ghost" onClick={onClose}>关闭</Button>}
    >
      {entries.length === 0 ? (
        <p className="small faint">索引为空或尚未成功拉取。</p>
      ) : (
        <div className="table-wrap">
          <table>
            <thead>
              <tr><th>Profile</th><th>厂商</th><th>版本</th><th className="right">操作</th></tr>
            </thead>
            <tbody>
              {entries.map((e) => (
                <tr key={e.id}>
                  <td>
                    <strong>{e.name}</strong>
                    <div className="faint small mono">{e.id}</div>
                  </td>
                  <td><Badge tone="info">{VENDOR_LABEL[e.vendor] ?? e.vendor}</Badge></td>
                  <td className="mono small">{e.version}</td>
                  <td className="right">
                    {installed.has(e.id) ? (
                      <Badge tone="good">已安装</Badge>
                    ) : (
                      <Button size="sm" onClick={() => onInstall(e.id)} disabled={busy}>
                        {busy ? <Loader2 size={12} className="spinner-icon" /> : <Download size={12} />} 安装
                      </Button>
                    )}
                  </td>
                </tr>
              ))}
            </tbody>
          </table>
        </div>
      )}
    </Modal>
  );
}

function ProfileDetail({ profile, onClose }: { profile: CloudProfile | null; onClose: () => void }) {
  const q = useQuery({
    queryKey: ['profile-detail', profile?.id],
    queryFn: () => profilesApi.get(profile!.id),
    enabled: Boolean(profile),
  });
  return (
    <Modal open={Boolean(profile)} title={profile?.name ?? ''} subtitle={profile?.id} onClose={onClose} width={760}
      footer={<Button variant="ghost" onClick={onClose}>关闭</Button>}>
      {q.isLoading ? <Spinner /> : <pre className="mono-block" style={{ maxHeight: 460 }}>{q.data?.document}</pre>}
    </Modal>
  );
}

function RunModal({ profile, onClose }: { profile: CloudProfile; onClose: () => void }) {
  const qc = useQueryClient();
  const toast = useToast();
  const doc = useQuery({ queryKey: ['profile-detail', profile.id], queryFn: () => profilesApi.get(profile.id) });
  const nodes = useQuery({ queryKey: ['admin-nodes'], queryFn: async () => (await api.nodes()).nodes });

  const [action, setAction] = useState<'ensure_port_allowed' | 'revoke_port_allowed'>('ensure_port_allowed');
  const [portMin, setPortMin] = useState('20000');
  const [portMax, setPortMax] = useState('20100');
  const [protocol, setProtocol] = useState('tcp');
  const [nodeId, setNodeId] = useState('');
  const [credential, setCredential] = useState('default');
  const [dryRun, setDryRun] = useState(true);
  const [ack, setAck] = useState(false);
  const [params, setParams] = useState<Record<string, string>>({});
  const [result, setResult] = useState<string | null>(null);

  const creds = useQuery({
    queryKey: ['profile-creds', profile.id],
    queryFn: () => profilesApi.credentials(profile.id),
  });

  const declared = useMemo(() => parseParameters(doc.data?.document ?? ''), [doc.data]);
  // Which fields this vendor needs, e.g. [access_token, subscription_id, ...].
  const credFields = useMemo(
    () => parseCredentialFields(doc.data?.document ?? ''),
    [doc.data],
  );

  // Inline credential editor. Without it the dropdown could only pick from
  // credentials that already existed, so a first-time user had no way in.
  const [editingCred, setEditingCred] = useState(false);
  const [credLabel, setCredLabel] = useState('default');
  const [credValues, setCredValues] = useState<Record<string, string>>({});

  const saveCred = useMutation({
    mutationFn: () =>
      profilesApi.setCredential(profile.id, credLabel.trim() || 'default', credValues),
    onSuccess: () => {
      toast.success('凭证已保存', '已加密存储，仅本服务端可读');
      setEditingCred(false);
      setCredValues({});
      void qc.invalidateQueries({ queryKey: ['profile-creds', profile.id] });
    },
    onError: (e) => toast.error('保存失败', e instanceof Error ? e.message : ''),
  });

  const run = useMutation({
    mutationFn: () =>
      profilesApi.run(profile.id, {
        action,
        node_id: nodeId || undefined,
        port_min: Number(portMin),
        port_max: Number(portMax),
        protocol,
        credential_label: credential,
        params: Object.fromEntries(Object.entries(params).filter(([, v]) => v !== '')),
        dry_run: dryRun,
        acknowledge_permissions: ack,
      }),
    onSuccess: (res) => {
      setResult(JSON.stringify(res, null, 2));
      toast.success(dryRun ? 'dry-run 完成' : '执行完成');
      void qc.invalidateQueries({ queryKey: ['profile-runs'] });
    },
    onError: (e) => {
      setResult(e instanceof Error ? e.message : '执行失败');
      toast.error('执行失败', e instanceof Error ? e.message : '');
    },
  });

  return (
    <Modal
      open
      title={`执行 ${profile.name}`}
      subtitle="放行前先做幂等检查；建议先用 dry-run 验证参数"
      onClose={onClose}
      width={760}
      footer={
        <>
          <Button variant="ghost" onClick={onClose}>取消</Button>
          <Button onClick={() => run.mutate()} disabled={run.isPending || (!dryRun && !ack)}>
            {run.isPending ? <Loader2 size={14} className="spinner-icon" /> : <Play size={14} />}
            {dryRun ? '执行 dry-run' : '执行'}
          </Button>
        </>
      }
    >
      <div className="grid grid-2">
        <Field label="动作">
          <select value={action} onChange={(e) => setAction(e.target.value as typeof action)}>
            <option value="ensure_port_allowed">ensure_port_allowed（放行）</option>
            <option value="revoke_port_allowed">revoke_port_allowed（回收）</option>
          </select>
        </Field>
        <Field label="关联节点（可选）" hint="节点区域会作为 region 参数默认值">
          <select value={nodeId} onChange={(e) => setNodeId(e.target.value)}>
            <option value="">不关联</option>
            {(nodes.data ?? []).map((n) => (
              <option key={n.id} value={n.id}>{n.name}{n.region ? ` · ${n.region}` : ''}</option>
            ))}
          </select>
        </Field>
      </div>
      <div className="grid grid-3">
        <Field label="起始端口"><input value={portMin} onChange={(e) => setPortMin(e.target.value.replace(/\D/g, ''))} /></Field>
        <Field label="结束端口"><input value={portMax} onChange={(e) => setPortMax(e.target.value.replace(/\D/g, ''))} /></Field>
        <Field label="协议">
          <select value={protocol} onChange={(e) => setProtocol(e.target.value)}>
            <option value="tcp">TCP</option>
            <option value="udp">UDP</option>
          </select>
        </Field>
      </div>
      <Field
        label="凭证"
        hint={
          creds.data?.credentials.length
            ? '凭证已加密存储'
            : `尚未配置凭证 — 需要 ${credFields.length ? credFields.join(' / ') : '该厂商的密钥字段'}`
        }
      >
        <div className="row gap-8">
          <select
            value={credential}
            onChange={(e) => setCredential(e.target.value)}
            style={{ flex: 1 }}
          >
            <option value="default">default</option>
            {(creds.data?.credentials ?? []).map((c) => (
              <option key={c.id} value={c.label}>{c.label}</option>
            ))}
          </select>
          <Button
            type="button"
            size="sm"
            variant="subtle"
            onClick={() => {
              setEditingCred((v) => !v);
              setCredLabel(credential);
            }}
          >
            {editingCred ? '收起' : creds.data?.credentials.length ? '管理' : '配置凭证'}
          </Button>
        </div>
      </Field>

      {editingCred && (
        <div className="card" style={{ padding: 14, display: 'flex', flexDirection: 'column', gap: 12 }}>
          <div className="small muted">
            为 <strong>{profile.id}</strong> 保存一份凭证。字段来自 profile 的
            <code className="mono"> auth.credential_fields </code>，加密后存于服务端数据库。
          </div>
          <Field label="凭证名称" hint="同一 profile 可保存多份，例如 default / prod">
            <input value={credLabel} onChange={(e) => setCredLabel(e.target.value)} placeholder="default" />
          </Field>
          {credFields.length === 0 ? (
            <div className="alert alert-warn">该 profile 未声明 credential_fields，无法配置凭证。</div>
          ) : (
            credFields.map((f) => (
              <Field key={f} label={f}>
                <input
                  type={/token|secret|key|password/i.test(f) ? 'password' : 'text'}
                  value={credValues[f] ?? ''}
                  onChange={(e) => setCredValues((p) => ({ ...p, [f]: e.target.value }))}
                  placeholder={f}
                  autoComplete="off"
                />
              </Field>
            ))
          )}
          <div className="row gap-8">
            <Button
              type="button"
              size="sm"
              onClick={() => saveCred.mutate()}
              disabled={saveCred.isPending || credFields.length === 0}
            >
              {saveCred.isPending ? '保存中…' : '保存凭证'}
            </Button>
            <Button type="button" size="sm" variant="ghost" onClick={() => setEditingCred(false)}>
              取消
            </Button>
          </div>
          {(creds.data?.credentials ?? []).length > 0 && (
            <div className="hint-list">
              {creds.data!.credentials.map((c) => (
                <div key={c.id} className="row gap-8 space-between">
                  <span className="mono small">{c.label}</span>
                  <button
                    type="button"
                    className="link-btn"
                    onClick={() => {
                      if (!confirm(`删除凭证 ${c.label}？`)) return;
                      void profilesApi.deleteCredential(profile.id, c.id).then(() => {
                        toast.success('已删除');
                        void qc.invalidateQueries({ queryKey: ['profile-creds', profile.id] });
                      });
                    }}
                  >
                    删除
                  </button>
                </div>
              ))}
            </div>
          )}
        </div>
      )}

      {declared.length > 0 && (
        <div className="grid grid-2">
          {declared.filter((p) => !['port_min', 'port_max', 'protocol'].includes(p.name)).map((p) => (
            <Field key={p.name} label={`${p.label}${p.required ? ' *' : ''}`} hint={p.description}>
              <input
                value={params[p.name] ?? ''}
                placeholder={p.example || p.default || ''}
                onChange={(e) => setParams({ ...params, [p.name]: e.target.value })}
              />
            </Field>
          ))}
        </div>
      )}

      <div className="row gap-16 wrap">
        <label className="checkbox-row">
          <input type="checkbox" checked={dryRun} onChange={(e) => setDryRun(e.target.checked)} />
          仅 dry-run（不调用云 API）
        </label>
        <label className="checkbox-row">
          <input type="checkbox" checked={ack} onChange={(e) => setAck(e.target.checked)} />
          我已确认该 profile 声明的云权限
        </label>
      </div>

      {result && <pre className="mono-block" style={{ maxHeight: 220 }}>{result}</pre>}
    </Modal>
  );
}

function SettingsModal({ initial, onClose }: { initial: ProfileSettings; onClose: () => void }) {
  const qc = useQueryClient();
  const toast = useToast();
  const [mirrors, setMirrors] = useState(initial.mirrors.join(', '));
  const [proxy, setProxy] = useState(initial.proxy);
  const [cidr, setCidr] = useState(initial.source_cidr);

  const save = useMutation({
    mutationFn: () => profilesApi.updateSettings({ mirrors, proxy, source_cidr: cidr }),
    onSuccess: () => {
      toast.success('已保存');
      void qc.invalidateQueries({ queryKey: ['profile-settings'] });
      void qc.invalidateQueries({ queryKey: ['profile-index'] });
      onClose();
    },
    onError: (e) => toast.error('保存失败', e instanceof Error ? e.message : ''),
  });

  return (
    <Modal
      open
      title="镜像与代理"
      subtitle="签名校验不可关闭；这里只影响从哪里下载"
      onClose={onClose}
      width={680}
      footer={
        <>
          <Button variant="ghost" onClick={onClose}>取消</Button>
          <Button onClick={() => save.mutate()} disabled={save.isPending}>保存</Button>
        </>
      }
    >
      <Field label="镜像链" hint="逗号分隔，按顺序回退；自定义镜像会优先于内置镜像">
        <textarea value={mirrors} onChange={(e) => setMirrors(e.target.value)} rows={3} />
      </Field>
      <Field label="HTTP / SOCKS5 代理" hint="例如 http://127.0.0.1:7890 或 socks5://127.0.0.1:1080">
        <input value={proxy} onChange={(e) => setProxy(e.target.value)} placeholder="留空表示直连" />
      </Field>
      <Field label="默认放行来源网段" hint="执行 profile 时 source_cidr 的默认值，建议收敛为固定 IP">
        <input value={cidr} onChange={(e) => setCidr(e.target.value)} placeholder="0.0.0.0/0" />
      </Field>
      <div className="alert alert-info small">
        <KeyRound size={15} />
        内置信任的公钥：{initial.trusted_keys.join(', ')}。镜像被篡改时验签会失败，不会执行任何请求。
      </div>
    </Modal>
  );
}



// parseParameters extracts spec.parameters from a profile YAML without a YAML
// dependency: the server already validated it, so a tolerant scan is enough for
// rendering the form.
function parseParameters(yaml: string): { name: string; label: string; required: boolean; default?: string; example?: string; description?: string }[] {
  const out: { name: string; label: string; required: boolean; default?: string; example?: string; description?: string }[] = [];
  const block = yaml.split('parameters:')[1];
  if (!block) return out;
  const lines = block.split('\n');
  let cur: Record<string, string> | null = null;
  const flush = () => {
    if (cur && cur.name) {
      out.push({
        name: cur.name,
        label: cur.label || cur.name,
        required: cur.required === 'true',
        default: cur.default,
        example: cur.example,
        description: cur.description,
      });
    }
  };
  for (const line of lines) {
    if (/^\s{0,2}\S/.test(line) && !line.trim().startsWith('-')) break;
    const m = line.match(/^\s*-?\s*(\w+):\s*(.*)$/);
    if (!m) continue;
    const [, key, rawVal] = m;
    const val = rawVal.replace(/^["']|["']$/g, '');
    if (key === 'name') {
      flush();
      cur = { name: val };
    } else if (cur) {
      cur[key] = val;
    }
  }
  flush();
  return out;
}

/**
 * parseCredentialFields reads spec.auth.credential_fields from a profile
 * document.
 *
 * The credential form has to know which fields a vendor expects — Azure wants
 * access_token/subscription_id/resource_group, Aliyun wants an AK pair — and
 * that list is the only source of truth. Without this the UI could offer a
 * dropdown of existing credentials but no way to create one.
 */
export function parseCredentialFields(yaml: string): string[] {
  const lines = yaml.split('\n');
  const start = lines.findIndex((l) => /^\s*credential_fields:/.test(l));
  if (start === -1) return [];
  const inline = lines[start].split('credential_fields:')[1]?.trim() ?? '';
  // Flow style: credential_fields: [a, b]
  if (inline.startsWith('[')) {
    return inline
      .replace(/^\[|\]$/g, '')
      .split(',')
      .map((s) => s.trim().replace(/^["']|["']$/g, ''))
      .filter(Boolean);
  }
  // Block style:
  //   credential_fields:
  //     - a
  //     - b
  const out: string[] = [];
  for (let i = start + 1; i < lines.length; i++) {
    const line = lines[i];
    const m = line.match(/^\s+-\s*(.+)$/);
    if (!m) {
      if (line.trim() === '') continue;
      break;
    }
    out.push(m[1].trim().replace(/^["']|["']$/g, ''));
  }
  return out;
}
