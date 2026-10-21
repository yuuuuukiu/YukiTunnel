import { useState } from 'react';
import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query';
import { KeyRound, Plus, ShieldOff, Trash2, UserCog } from 'lucide-react';
import { api } from '../../lib/api';
import type { User } from '../../lib/types';
import { useAuth } from '../../state/auth';
import { useToast } from '../../state/toast';
import { Badge, Button, Card, CardHeader, Field, Modal, Spinner, Switch } from '../../components/ui';
import { formatTime, timeAgo } from '../../lib/format';

export default function AdminUsersPage() {
  const qc = useQueryClient();
  const toast = useToast();
  const { user: me } = useAuth();
  const users = useQuery({ queryKey: ['admin-users'], queryFn: api.users });

  const [createOpen, setCreateOpen] = useState(false);
  const [username, setUsername] = useState('');
  const [display, setDisplay] = useState('');
  const [password, setPassword] = useState('');
  const [role, setRole] = useState('user');
  const [pubkey, setPubkey] = useState('');
  const [keyName, setKeyName] = useState('');
  const [deleteTarget, setDeleteTarget] = useState<User | null>(null);
  const [keysTarget, setKeysTarget] = useState<User | null>(null);

  const create = useMutation({
    mutationFn: () =>
      api.createUser({
        username: username.trim(),
        display_name: display.trim() || undefined,
        password: password || undefined,
        role,
        public_key: pubkey.trim() || undefined,
        key_name: keyName || undefined,
      }),
    onSuccess: () => {
      toast.success('用户已创建');
      setCreateOpen(false);
      setUsername('');
      setDisplay('');
      setPassword('');
      setPubkey('');
      setRole('user');
      void qc.invalidateQueries({ queryKey: ['admin-users'] });
    },
    onError: (e) => toast.error('创建失败', e instanceof Error ? e.message : ''),
  });

  const update = useMutation({
    mutationFn: ({ id, body }: { id: string; body: Record<string, unknown> }) => api.updateUser(id, body),
    onSuccess: () => {
      toast.success('已更新');
      void qc.invalidateQueries({ queryKey: ['admin-users'] });
    },
    onError: (e) => toast.error('更新失败', e instanceof Error ? e.message : ''),
  });

  const remove = useMutation({
    mutationFn: (id: string) => api.deleteUser(id),
    onSuccess: () => {
      toast.success('用户已删除');
      setDeleteTarget(null);
      void qc.invalidateQueries({ queryKey: ['admin-users'] });
    },
    onError: (e) => toast.error('删除失败', e instanceof Error ? e.message : ''),
  });

  return (
    <>
      <div className="page-head">
        <div>
          <h1>用户管理</h1>
          <p>账号由管理员创建，不开放自助注册。可同时配置密码与 RSA 公钥。</p>
        </div>
        <Button onClick={() => setCreateOpen(true)}><Plus size={15} /> 新建用户</Button>
      </div>

      <Card>
        <CardHeader title={`全部用户（${users.data?.users.length ?? 0}）`} subtitle="禁用后立即拒绝登录并断开控制连接" />
        {users.isLoading ? (
          <Spinner />
        ) : (
          <div className="table-wrap">
            <table>
              <thead>
                <tr>
                  <th>用户</th>
                  <th>角色</th>
                  <th>密钥</th>
                  <th>隧道</th>
                  <th>最近登录</th>
                  <th>启用</th>
                  <th className="right">操作</th>
                </tr>
              </thead>
              <tbody>
                {(users.data?.users ?? []).map((u) => (
                  <tr key={u.id}>
                    <td>
                      <strong>{u.username}</strong>
                      <div className="faint small">{u.display_name || '—'} · {u.id}</div>
                    </td>
                    <td>
                      <select
                        value={u.role}
                        disabled={u.id === me?.id}
                        onChange={(e) => update.mutate({ id: u.id, body: { role: e.target.value } })}
                        style={{ width: 100 }}
                      >
                        <option value="user">普通用户</option>
                        <option value="admin">管理员</option>
                      </select>
                    </td>
                    <td>{u.key_count ?? 0}</td>
                    <td>{u.tunnel_count ?? 0}</td>
                    <td className="small muted">{timeAgo(u.last_login_at)}</td>
                    <td>
                      <Switch
                        checked={!u.disabled}
                        disabled={u.id === me?.id}
                        label={`启用 ${u.username}`}
                        onChange={(v) => update.mutate({ id: u.id, body: { disabled: !v } })}
                      />
                    </td>
                    <td className="right">
                      <div className="row gap-8" style={{ justifyContent: 'flex-end' }}>
                        <button className="icon-btn" title="公钥" onClick={() => setKeysTarget(u)}><KeyRound size={15} /></button>
                        <button className="icon-btn" title="重置密码" onClick={() => {
                          const pw = window.prompt(`为 ${u.username} 设置新密码（至少 8 位）`);
                          if (pw) update.mutate({ id: u.id, body: { password: pw } });
                        }}><UserCog size={15} /></button>
                        <button className="icon-btn" title="删除" disabled={u.id === me?.id} onClick={() => setDeleteTarget(u)}><Trash2 size={15} /></button>
                      </div>
                    </td>
                  </tr>
                ))}
              </tbody>
            </table>
          </div>
        )}
      </Card>

      <Modal
        open={createOpen}
        title="新建用户"
        subtitle="密码与公钥至少填写一项"
        onClose={() => setCreateOpen(false)}
        footer={
          <>
            <Button variant="ghost" onClick={() => setCreateOpen(false)}>取消</Button>
            <Button onClick={() => create.mutate()} disabled={create.isPending || !username.trim()}>创建</Button>
          </>
        }
      >
        <div className="grid grid-2">
          <Field label="用户名">
            <input value={username} onChange={(e) => setUsername(e.target.value)} placeholder="alice" />
          </Field>
          <Field label="显示名称（可选）">
            <input value={display} onChange={(e) => setDisplay(e.target.value)} placeholder="Alice" />
          </Field>
        </div>
        <div className="grid grid-2">
          <Field label="初始密码" hint="至少 8 位；留空则仅允许 Key 登录">
            <input type="password" value={password} onChange={(e) => setPassword(e.target.value)} />
          </Field>
          <Field label="角色">
            <select value={role} onChange={(e) => setRole(e.target.value)}>
              <option value="user">普通用户</option>
              <option value="admin">管理员</option>
            </select>
          </Field>
        </div>
        <Field label="RSA / SSH 公钥（可选）" hint="粘贴 ssh-rsa AAAA... 内容">
          <textarea value={pubkey} onChange={(e) => setPubkey(e.target.value)} rows={4} />
        </Field>
        <Field label="密钥名称（可选）">
          <input value={keyName} onChange={(e) => setKeyName(e.target.value)} placeholder="Alice 的笔记本" />
        </Field>
      </Modal>

      <KeysModal user={keysTarget} onClose={() => setKeysTarget(null)} />

      <Modal
        open={Boolean(deleteTarget)}
        title="删除用户"
        onClose={() => setDeleteTarget(null)}
        footer={
          <>
            <Button variant="ghost" onClick={() => setDeleteTarget(null)}>取消</Button>
            <Button variant="danger" onClick={() => deleteTarget && remove.mutate(deleteTarget.id)}>确认删除</Button>
          </>
        }
      >
        <p>删除用户 <strong>{deleteTarget?.username}</strong> 会同时删除其全部隧道与密钥，且不可恢复。</p>
        <div className="alert alert-warn"><ShieldOff size={16} /> 请先确认该用户没有正在使用的服务。</div>
      </Modal>
    </>
  );
}

function KeysModal({ user, onClose }: { user: User | null; onClose: () => void }) {
  const qc = useQueryClient();
  const toast = useToast();
  const [pubkey, setPubkey] = useState('');
  const [name, setName] = useState('');
  const keys = useQuery({
    queryKey: ['admin-user-keys', user?.id],
    queryFn: () => api.userKeys(user!.id),
    enabled: Boolean(user),
  });

  const add = useMutation({
    mutationFn: () => api.addUserKey(user!.id, name, pubkey),
    onSuccess: () => {
      toast.success('公钥已添加');
      setPubkey('');
      setName('');
      void qc.invalidateQueries({ queryKey: ['admin-user-keys', user?.id] });
      void qc.invalidateQueries({ queryKey: ['admin-users'] });
    },
    onError: (e) => toast.error('添加失败', e instanceof Error ? e.message : ''),
  });

  const del = useMutation({
    mutationFn: (id: string) => api.deleteUserKey(user!.id, id),
    onSuccess: () => {
      void qc.invalidateQueries({ queryKey: ['admin-user-keys', user?.id] });
      void qc.invalidateQueries({ queryKey: ['admin-users'] });
    },
  });

  return (
    <Modal
      open={Boolean(user)}
      title={`${user?.username ?? ''} 的公钥`}
      subtitle="登记后该用户即可使用对应私钥登录"
      onClose={onClose}
      footer={<Button variant="ghost" onClick={onClose}>关闭</Button>}
    >
      {keys.isLoading ? (
        <Spinner />
      ) : (keys.data?.keys.length ?? 0) === 0 ? (
        <p className="small faint">暂无公钥</p>
      ) : (
        <div className="hint-list">
          {keys.data!.keys.map((k) => (
            <div key={k.id} className="row gap-8" style={{ padding: '6px 0', borderBottom: '1px solid var(--line-soft)' }}>
              <div style={{ minWidth: 0 }}>
                <div style={{ fontWeight: 600 }}>{k.name || '未命名'}</div>
                <div className="mono faint small" style={{ wordBreak: 'break-all' }}>{k.fingerprint}</div>
                <div className="faint small">登记于 {formatTime(k.created_at)}</div>
              </div>
              <span className="grow" />
              <button className="icon-btn" onClick={() => del.mutate(k.id)}><Trash2 size={14} /></button>
            </div>
          ))}
        </div>
      )}
      <div className="divider" />
      <Field label="新增公钥">
        <textarea value={pubkey} onChange={(e) => setPubkey(e.target.value)} rows={3} placeholder="ssh-rsa AAAA..." />
      </Field>
      <div className="row gap-8">
        <input value={name} onChange={(e) => setName(e.target.value)} placeholder="密钥名称（可选）" />
        <Button onClick={() => add.mutate()} disabled={!pubkey.trim() || add.isPending}>添加</Button>
      </div>
      <Badge tone="info">提示：RSA 与 Ed25519 公钥都可登记，登录时使用对应私钥签名。</Badge>
    </Modal>
  );
}
