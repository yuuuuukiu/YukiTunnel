import { useState } from 'react';
import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query';
import { KeyRound, Plus, Trash2 } from 'lucide-react';
import { api } from '../lib/api';
import { useAuth } from '../state/auth';
import { useToast } from '../state/toast';
import { Badge, Button, Card, CardHeader, Field, Modal, Spinner } from '../components/ui';
import { formatTime } from '../lib/format';

export default function AccountPage() {
  const { user } = useAuth();
  const qc = useQueryClient();
  const toast = useToast();
  const keys = useQuery({ queryKey: ['my-keys'], queryFn: api.myKeys });

  const [pwdOpen, setPwdOpen] = useState(false);
  const [current, setCurrent] = useState('');
  const [next, setNext] = useState('');
  const [keyOpen, setKeyOpen] = useState(false);
  const [keyName, setKeyName] = useState('');
  const [pubkey, setPubkey] = useState('');

  const changePwd = useMutation({
    mutationFn: () => api.changePassword(current, next),
    onSuccess: () => {
      toast.success('密码已更新');
      setPwdOpen(false);
      setCurrent('');
      setNext('');
    },
    onError: (e) => toast.error('修改失败', e instanceof Error ? e.message : ''),
  });

  const addKey = useMutation({
    mutationFn: () => api.addMyKey(keyName, pubkey),
    onSuccess: () => {
      toast.success('公钥已登记');
      setKeyOpen(false);
      setKeyName('');
      setPubkey('');
      void qc.invalidateQueries({ queryKey: ['my-keys'] });
    },
    onError: (e) => toast.error('登记失败', e instanceof Error ? e.message : ''),
  });

  const removeKey = useMutation({
    mutationFn: (id: string) => api.deleteMyKey(id),
    onSuccess: () => {
      toast.success('已删除密钥');
      void qc.invalidateQueries({ queryKey: ['my-keys'] });
    },
  });

  return (
    <>
      <div className="page-head">
        <div>
          <h1>账号与密钥</h1>
          <p>密码与 RSA Key 均可用于登录；建议至少保留一种可用的认证方式。</p>
        </div>
      </div>

      <div className="grid grid-2">
        <Card>
          <CardHeader title="账号信息" />
          <dl className="kv">
            <dt>用户名</dt><dd>{user?.username}</dd>
            <dt>显示名称</dt><dd>{user?.display_name || '—'}</dd>
            <dt>角色</dt><dd>{user?.role === 'admin' ? <Badge tone="info">管理员</Badge> : <Badge tone="neutral">普通用户</Badge>}</dd>
            <dt>创建时间</dt><dd>{formatTime(user?.created_at ?? '')}</dd>
            <dt>最近登录</dt><dd>{formatTime(user?.last_login_at ?? '')}</dd>
          </dl>
          <div className="row gap-8" style={{ marginTop: 16 }}>
            <Button variant="subtle" size="sm" onClick={() => setPwdOpen(true)}>修改密码</Button>
          </div>
        </Card>

        <Card>
          <CardHeader
            title="RSA / SSH 公钥"
            subtitle="登记公钥后即可使用私钥登录（无需密码）"
            actions={<Button size="sm" variant="subtle" onClick={() => setKeyOpen(true)}><Plus size={13} /> 添加</Button>}
          />
          {keys.isLoading ? (
            <Spinner />
          ) : (keys.data?.keys.length ?? 0) === 0 ? (
            <p className="small faint">尚未登记公钥。运行 <span className="mono">ssh-keygen -t rsa</span> 生成密钥，然后粘贴公钥。</p>
          ) : (
            <div className="hint-list">
              {keys.data!.keys.map((k) => (
                <div key={k.id} className="row gap-8" style={{ padding: '8px 0', borderBottom: '1px solid var(--line-soft)' }}>
                  <KeyRound size={15} className="faint" />
                  <div style={{ minWidth: 0 }}>
                    <div style={{ fontWeight: 600 }}>{k.name || '未命名密钥'}</div>
                    <div className="mono faint small" style={{ wordBreak: 'break-all' }}>{k.fingerprint}</div>
                  </div>
                  <span className="grow" />
                  <button className="icon-btn" title="删除" onClick={() => removeKey.mutate(k.id)}>
                    <Trash2 size={14} />
                  </button>
                </div>
              ))}
            </div>
          )}
        </Card>
      </div>

      <Modal
        open={pwdOpen}
        title="修改密码"
        onClose={() => setPwdOpen(false)}
        footer={
          <>
            <Button variant="ghost" onClick={() => setPwdOpen(false)}>取消</Button>
            <Button onClick={() => changePwd.mutate()} disabled={changePwd.isPending || next.length < 8}>保存</Button>
          </>
        }
      >
        <Field label="当前密码">
          <input type="password" value={current} onChange={(e) => setCurrent(e.target.value)} />
        </Field>
        <Field label="新密码" hint="至少 8 位">
          <input type="password" value={next} onChange={(e) => setNext(e.target.value)} />
        </Field>
      </Modal>

      <Modal
        open={keyOpen}
        title="登记公钥"
        onClose={() => setKeyOpen(false)}
        footer={
          <>
            <Button variant="ghost" onClick={() => setKeyOpen(false)}>取消</Button>
            <Button onClick={() => addKey.mutate()} disabled={addKey.isPending || !pubkey.trim()}>登记</Button>
          </>
        }
      >
        <Field label="名称" hint="便于区分设备，例如 MacBook">
          <input value={keyName} onChange={(e) => setKeyName(e.target.value)} placeholder="工作电脑" />
        </Field>
        <Field label="公钥" hint="粘贴 ssh-rsa AAAA... 或 ssh-ed25519 AAAA... 内容">
          <textarea value={pubkey} onChange={(e) => setPubkey(e.target.value)} rows={5} />
        </Field>
      </Modal>
    </>
  );
}
