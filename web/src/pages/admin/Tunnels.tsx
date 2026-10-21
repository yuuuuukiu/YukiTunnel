import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query';
import { useState } from 'react';
import { Search, Trash2 } from 'lucide-react';
import { api } from '../../lib/api';
import { useToast } from '../../state/toast';
import { Badge, Button, Card, CardHeader, Spinner, Switch } from '../../components/ui';
import { humanBytes } from '../../lib/format';

export default function AdminTunnelsPage() {
  const qc = useQueryClient();
  const toast = useToast();
  const [filter, setFilter] = useState('');
  const tunnels = useQuery({ queryKey: ['admin-tunnels'], queryFn: () => api.adminTunnels(), refetchInterval: 8000 });
  const users = useQuery({ queryKey: ['admin-users'], queryFn: api.users });

  const toggle = useMutation({
    mutationFn: ({ id, enabled }: { id: string; enabled: boolean }) => api.adminToggleTunnel(id, enabled),
    onSuccess: () => {
      void qc.invalidateQueries({ queryKey: ['admin-tunnels'] });
    },
    onError: (e) => toast.error('操作失败', e instanceof Error ? e.message : ''),
  });

  const remove = useMutation({
    mutationFn: (id: string) => api.adminDeleteTunnel(id),
    onSuccess: () => {
      toast.success('隧道已删除');
      void qc.invalidateQueries({ queryKey: ['admin-tunnels'] });
    },
    onError: (e) => toast.error('删除失败', e instanceof Error ? e.message : ''),
  });

  const userName = (id: string) => users.data?.users.find((u) => u.id === id)?.username ?? id.slice(0, 8);
  const list = (tunnels.data?.tunnels ?? []).filter((t) => {
    if (!filter) return true;
    const q = filter.toLowerCase();
    return t.name.toLowerCase().includes(q) || t.address.toLowerCase().includes(q) || userName(t.user_id).toLowerCase().includes(q);
  });

  return (
    <>
      <div className="page-head">
        <div>
          <h1>隧道管理</h1>
          <p>查看所有用户的隧道。禁用会立即关闭公网监听，用户无法自行恢复。</p>
        </div>
        <div className="row gap-8">
          <div className="row gap-8" style={{ position: 'relative' }}>
            <Search size={15} className="faint" style={{ position: 'absolute', left: 10 }} />
            <input
              value={filter}
              onChange={(e) => setFilter(e.target.value)}
              placeholder="搜索名称 / 地址 / 用户"
              style={{ paddingLeft: 32, width: 240 }}
            />
          </div>
        </div>
      </div>

      <Card>
        <CardHeader title={`全部隧道（${list.length}）`} subtitle="每 8 秒刷新一次状态与流量" />
        {tunnels.isLoading ? (
          <Spinner />
        ) : (
          <div className="table-wrap">
            <table>
              <thead>
                <tr>
                  <th>名称</th>
                  <th>用户</th>
                  <th>节点</th>
                  <th>协议</th>
                  <th>内网</th>
                  <th>公网地址</th>
                  <th>流量</th>
                  <th>状态</th>
                  <th>启用</th>
                  <th className="right">操作</th>
                </tr>
              </thead>
              <tbody>
                {list.map((t) => (
                  <tr key={t.id}>
                    <td><strong>{t.name}</strong><div className="faint small">{t.id}</div></td>
                    <td className="small">{userName(t.user_id)}</td>
                    <td className="small">{t.node_name}</td>
                    <td><Badge tone="neutral">{t.proto.toUpperCase()}</Badge></td>
                    <td className="mono small">{t.local_ip}:{t.local_port}</td>
                    <td className="mono small">{t.address}</td>
                    <td className="mono small">{humanBytes(t.rx_bytes + t.tx_bytes)}</td>
                    <td>{t.active ? <Badge tone="good">运行中</Badge> : <Badge tone="neutral">已停止</Badge>}</td>
                    <td>
                      <Switch checked={t.enabled} label={`启用 ${t.name}`} onChange={(v) => toggle.mutate({ id: t.id, enabled: v })} />
                    </td>
                    <td className="right">
                      <button
                        className="icon-btn"
                        title="删除"
                        onClick={() => {
                          if (window.confirm(`确认删除隧道 ${t.name}？`)) remove.mutate(t.id);
                        }}
                      >
                        <Trash2 size={15} />
                      </button>
                    </td>
                  </tr>
                ))}
              </tbody>
            </table>
          </div>
        )}
        <div className="row gap-8" style={{ marginTop: 12 }}>
          <Button variant="ghost" size="sm" onClick={() => void tunnels.refetch()}>刷新</Button>
        </div>
      </Card>
    </>
  );
}
