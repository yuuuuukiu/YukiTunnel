import { useState } from 'react';
import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query';
import { Plus, RefreshCw, Trash2, Wifi } from 'lucide-react';
import { api } from '../../lib/api';
import type { AdminNode } from '../../lib/types';
import { useToast } from '../../state/toast';
import { Badge, Button, Card, CardHeader, Field, Modal, Spinner, Switch } from '../../components/ui';
import { latencyClass, timeAgo } from '../../lib/format';

interface NodeForm {
  name: string;
  region: string;
  public_host: string;
  control_host: string;
  control_port: string;
  max_tunnels: string;
}

const emptyForm: NodeForm = { name: '', region: '', public_host: '', control_host: '', control_port: '9443', max_tunnels: '0' };

export default function AdminNodesPage() {
  const qc = useQueryClient();
  const toast = useToast();
  const nodes = useQuery({ queryKey: ['admin-nodes'], queryFn: api.nodes, refetchInterval: 15000 });

  const [form, setForm] = useState<NodeForm>(emptyForm);
  const [editing, setEditing] = useState<AdminNode | null>(null);
  const [open, setOpen] = useState(false);
  const [checking, setChecking] = useState<string | null>(null);

  const save = useMutation({
    mutationFn: async () => {
      const body = {
        name: form.name.trim(),
        region: form.region.trim(),
        public_host: form.public_host.trim(),
        control_host: form.control_host.trim(),
        control_port: Number(form.control_port) || 0,
        max_tunnels: Number(form.max_tunnels) || 0,
      };
      if (editing) return api.updateNode(editing.id, body);
      return api.createNode(body);
    },
    onSuccess: () => {
      toast.success(editing ? '节点已更新' : '节点已创建');
      setOpen(false);
      setEditing(null);
      setForm(emptyForm);
      void qc.invalidateQueries({ queryKey: ['admin-nodes'] });
    },
    onError: (e) => toast.error('保存失败', e instanceof Error ? e.message : ''),
  });

  const toggle = useMutation({
    mutationFn: ({ id, enabled }: { id: string; enabled: boolean }) => api.updateNode(id, { enabled }),
    onSuccess: () => void qc.invalidateQueries({ queryKey: ['admin-nodes'] }),
    onError: (e) => toast.error('更新失败', e instanceof Error ? e.message : ''),
  });

  const remove = useMutation({
    mutationFn: (id: string) => api.deleteNode(id),
    onSuccess: () => {
      toast.success('节点已删除');
      void qc.invalidateQueries({ queryKey: ['admin-nodes'] });
    },
    onError: (e) => toast.error('删除失败', e instanceof Error ? e.message : ''),
  });

  const check = async (n: AdminNode) => {
    setChecking(n.id);
    try {
      const res = await api.checkNode(n.id);
      toast[res.status === 'online' ? 'success' : 'error'](
        res.status === 'online' ? '节点在线' : '节点离线',
        `${n.name} · ${res.latency_ms} ms`,
      );
      void qc.invalidateQueries({ queryKey: ['admin-nodes'] });
    } catch (e) {
      toast.error('检测失败', e instanceof Error ? e.message : '');
    } finally {
      setChecking(null);
    }
  };

  const startEdit = (n: AdminNode) => {
    setEditing(n);
    setForm({
      name: n.name,
      region: n.region,
      public_host: n.public_host,
      control_host: n.control_host,
      control_port: String(n.control_port),
      max_tunnels: String(n.max_tunnels),
    });
    setOpen(true);
  };

  return (
    <>
      <div className="page-head">
        <div>
          <h1>节点管理</h1>
          <p>节点代表一个公网入口。客户端创建隧道时会在节点列表中选择，并显示其延迟。</p>
        </div>
        <Button onClick={() => { setEditing(null); setForm(emptyForm); setOpen(true); }}>
          <Plus size={15} /> 添加节点
        </Button>
      </div>

      <Card>
        <CardHeader title="全部节点" subtitle="停用节点会立即停止其上的隧道" />
        {nodes.isLoading ? (
          <Spinner />
        ) : (
          <div className="table-wrap">
            <table>
              <thead>
                <tr>
                  <th>名称</th>
                  <th>公网入口</th>
                  <th>控制地址</th>
                  <th>状态 / 延迟</th>
                  <th>隧道上限</th>
                  <th>启用</th>
                  <th className="right">操作</th>
                </tr>
              </thead>
              <tbody>
                {(nodes.data?.nodes ?? []).map((n) => (
                  <tr key={n.id}>
                    <td>
                      <strong>{n.name}</strong>
                      <div className="faint small">{n.region || '默认'} · {n.id}</div>
                    </td>
                    <td className="mono small">{n.public_host}</td>
                    <td className="mono small">{n.control_host || n.public_host}:{n.control_port}</td>
                    <td>
                      {n.status === 'online' ? <Badge tone="good">在线</Badge> : n.status === 'offline' ? <Badge tone="bad">离线</Badge> : <Badge tone="neutral">未知</Badge>}
                      <span className={`latency latency-${latencyClass(n.latency_ms)}`} style={{ marginLeft: 8 }}>
                        {n.latency_ms ? `${n.latency_ms} ms` : '—'}
                      </span>
                      <div className="faint small">最近 {timeAgo(n.last_seen_at)}</div>
                    </td>
                    <td>{n.max_tunnels || '不限'}</td>
                    <td>
                      <Switch checked={n.enabled} label={`启用 ${n.name}`} onChange={(v) => toggle.mutate({ id: n.id, enabled: v })} />
                    </td>
                    <td className="right">
                      <div className="row gap-8" style={{ justifyContent: 'flex-end' }}>
                        <button className="icon-btn" title="检测" onClick={() => check(n)} disabled={checking === n.id}>
                          {checking === n.id ? <RefreshCw size={15} className="spinner-icon" /> : <Wifi size={15} />}
                        </button>
                        <Button size="sm" variant="ghost" onClick={() => startEdit(n)}>编辑</Button>
                        <button className="icon-btn" title="删除" onClick={() => {
                          if (window.confirm(`确认删除节点 ${n.name}？`)) remove.mutate(n.id);
                        }}><Trash2 size={15} /></button>
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
        open={open}
        title={editing ? '编辑节点' : '添加节点'}
        subtitle="公网入口是客户端最终访问的 IP 或域名"
        onClose={() => setOpen(false)}
        footer={
          <>
            <Button variant="ghost" onClick={() => setOpen(false)}>取消</Button>
            <Button onClick={() => save.mutate()} disabled={save.isPending || !form.name.trim() || !form.public_host.trim()}>保存</Button>
          </>
        }
      >
        <div className="grid grid-2">
          <Field label="节点名称" hint="唯一，例如 hk-1">
            <input value={form.name} onChange={(e) => setForm({ ...form, name: e.target.value })} placeholder="hk-1" />
          </Field>
          <Field label="区域（可选）">
            <input value={form.region} onChange={(e) => setForm({ ...form, region: e.target.value })} placeholder="香港" />
          </Field>
        </div>
        <Field label="公网入口（IP 或域名）" hint="客户端看到的访问地址将使用此值（域名绑定关闭时）">
          <input value={form.public_host} onChange={(e) => setForm({ ...form, public_host: e.target.value })} placeholder="203.0.113.10" />
        </Field>
        <div className="grid grid-2">
          <Field label="控制地址（可选）" hint="留空则与公网入口相同；用于延迟探测">
            <input value={form.control_host} onChange={(e) => setForm({ ...form, control_host: e.target.value })} placeholder="10.0.0.5" />
          </Field>
          <Field label="控制端口">
            <input value={form.control_port} onChange={(e) => setForm({ ...form, control_port: e.target.value.replace(/\D/g, '') })} placeholder="9443" />
          </Field>
        </div>
        <Field label="隧道数量上限" hint="0 表示不限制">
          <input value={form.max_tunnels} onChange={(e) => setForm({ ...form, max_tunnels: e.target.value.replace(/\D/g, '') })} placeholder="0" />
        </Field>
      </Modal>
    </>
  );
}
