import { useEffect, useMemo, useState } from 'react';
import { useQuery, useQueryClient } from '@tanstack/react-query';
import { useNavigate } from 'react-router-dom';
import {
  Activity, Copy, LogOut, Pause, Pencil, Play, Plus, RefreshCw, ScrollText, Server,
} from 'lucide-react';
import { api } from '@web/lib/api';
import { useAuth } from '@web/state/auth';
import { useToast } from '@web/state/toast';
import { Badge, Button, Card, CardHeader, EmptyState, Modal, Spinner } from '@web/components/ui';
import { copyText, humanBytes, latencyClass } from '@web/lib/format';
import type { Tunnel } from '@web/lib/types';
import { CreateTunnelModal } from '@web/components/CreateTunnelModal';
import { AgentControl } from '../components/AgentControl';

export default function ClientHomePage() {
  const { user, logout, serverUrl } = useAuth();
  const qc = useQueryClient();
  const toast = useToast();
  const navigate = useNavigate();
  const [creating, setCreating] = useState(false);
  const [editing, setEditing] = useState<Tunnel | null>(null);
  const [logsFor, setLogsFor] = useState<Tunnel | null>(null);
  const [busy, setBusy] = useState<string | null>(null);

  const servers = useQuery({ queryKey: ['servers'], queryFn: api.servers, refetchInterval: 15000 });
  const tunnels = useQuery({ queryKey: ['tunnels'], queryFn: api.tunnels, refetchInterval: 3000 });

  const list = useMemo(() => tunnels.data?.tunnels ?? [], [tunnels.data]);
  const active = list.filter((t) => t.active).length;

  const toggle = async (t: Tunnel) => {
    setBusy(t.id);
    try {
      if (t.active) await api.stopTunnel(t.id);
      else await api.startTunnel(t.id);
      toast.success(t.active ? '隧道已停止' : '隧道已启动', t.address);
      void qc.invalidateQueries({ queryKey: ['tunnels'] });
    } catch (e) {
      toast.error(t.active ? '停止失败' : '启动失败', e instanceof Error ? e.message : '未知错误');
    } finally {
      setBusy(null);
    }
  };

  return (
    <>
      <div className="page-head">
        <div>
          <h1>我的隧道</h1>
          <p className="row gap-8 small muted">
            <Server size={13} /> {serverUrl ?? '本地服务端'} · {user?.username}
            <Badge tone="good"><span className="dot dot-good" /> 已登录</Badge>
          </p>
        </div>
        <div className="row gap-8">
          <Button variant="ghost" onClick={() => void tunnels.refetch()}><RefreshCw size={15} /> 刷新</Button>
          <Button onClick={() => setCreating(true)}><Plus size={16} /> 新建隧道</Button>
        </div>
      </div>

      <AgentControl tunnelCount={list.length} activeCount={active} />

      <div className="grid grid-2">
        <Card>
          <CardHeader
            title="可用节点"
            subtitle="延迟为服务端到节点的实时探测值"
            actions={<Badge tone={servers.data?.domain_binding ? 'good' : 'neutral'}>域名绑定 {servers.data?.domain_binding ? '开' : '关'}</Badge>}
          />
          {servers.isLoading ? (
            <Spinner />
          ) : (
            <div className="hint-list">
              {(servers.data?.servers ?? []).map((s) => (
                <div key={s.id} className="row gap-8" style={{ padding: '6px 0', borderBottom: '1px solid var(--line-soft)' }}>
                  <span className={`dot ${s.online ? 'dot-good' : 'dot-bad'}`} />
                  <strong>{s.name}</strong>
                  <span className="faint small">{s.region || '默认'} · {s.public_host}</span>
                  <span className="grow" />
                  <span className={`latency latency-${latencyClass(s.latency_ms)}`}>{s.latency_ms} ms</span>
                </div>
              ))}
              {(servers.data?.servers.length ?? 0) === 0 && <p className="faint small">暂无节点，请联系管理员。</p>}
            </div>
          )}
        </Card>

        <Card>
          <CardHeader title="账号" subtitle="令牌保存在系统配置目录，仅本机可读" />
          <dl className="kv">
            <dt>用户名</dt><dd>{user?.username}</dd>
            <dt>角色</dt><dd>{user?.role === 'admin' ? '管理员' : '普通用户'}</dd>
            <dt>服务端</dt><dd className="mono small">{serverUrl}</dd>
          </dl>
          <div className="row gap-8" style={{ marginTop: 14 }}>
            <Button variant="ghost" size="sm" onClick={() => navigate('/settings')}>连接设置</Button>
            <Button
              variant="danger"
              size="sm"
              onClick={async () => {
                await logout();
                toast.info('已退出登录');
              }}
            >
              <LogOut size={13} /> 退出登录
            </Button>
          </div>
        </Card>
      </div>

      <Card>
        <CardHeader title={`隧道（${list.length}）`} subtitle="点击启动后，公网地址立即可用" />
        {tunnels.isLoading ? (
          <Spinner label="加载隧道…" />
        ) : list.length === 0 ? (
          <EmptyState
            icon={<Activity size={28} />}
            title="还没有隧道"
            detail="点击「新建隧道」，选择节点并填写内网服务地址。"
            action={<Button onClick={() => setCreating(true)}><Plus size={15} /> 新建隧道</Button>}
          />
        ) : (
          <div className="table-wrap">
            <table>
              <thead>
                <tr>
                  <th>隧道</th>
                  <th>公网地址</th>
                  <th>流量</th>
                  <th>状态</th>
                  <th className="right">操作</th>
                </tr>
              </thead>
              <tbody>
                {list.map((t) => (
                  <tr key={t.id}>
                    <td>
                      <div className="row gap-8">
                        <strong>{t.name}</strong>
                        <Badge tone="neutral">{t.proto.toUpperCase()}</Badge>
                      </div>
                      <div className="faint small">
                        {t.node_name} · {t.local_ip}:{t.local_port}
                      </div>
                    </td>
                    <td>
                      <div className="row gap-8">
                        <span className="mono small">{t.address}</span>
                        <button
                          className="icon-btn"
                          title="复制地址"
                          onClick={() => {
                            void copyText(t.address);
                            toast.success('已复制访问地址');
                          }}
                        >
                          <Copy size={13} />
                        </button>
                      </div>
                    </td>
                    <td className="mono small">
                      <div className="traffic-cell">
                        <span>↑ {humanBytes(t.rx_bytes)}</span>
                        <span>↓ {humanBytes(t.tx_bytes)}</span>
                      </div>
                    </td>
                    <td>
                      {t.active ? <Badge tone="good">运行中</Badge> : <Badge tone="neutral">已停止</Badge>}
                    </td>
                    <td className="right">
                      <div className="row gap-8" style={{ justifyContent: 'flex-end' }}>
                        <Button
                          size="sm"
                          variant={t.active ? 'subtle' : 'primary'}
                          disabled={busy === t.id}
                          onClick={() => void toggle(t)}
                        >
                          {t.active ? <Pause size={12} /> : <Play size={12} />}
                          {t.active ? '停止' : '启动'}
                        </Button>
                        <button className="icon-btn" title="运行日志" onClick={() => setLogsFor(t)}>
                          <ScrollText size={14} />
                        </button>
                        <button className="icon-btn" title="编辑" onClick={() => setEditing(t)}>
                          <Pencil size={14} />
                        </button>
                      </div>
                    </td>
                  </tr>
                ))}
              </tbody>
            </table>
          </div>
        )}
      </Card>

      <CreateTunnelModal open={creating} onClose={() => setCreating(false)} />
      <CreateTunnelModal open={Boolean(editing)} initial={editing} onClose={() => setEditing(null)} />
      <TunnelLogModal tunnel={logsFor} onClose={() => setLogsFor(null)} />
    </>
  );
}

function TunnelLogModal({ tunnel, onClose }: { tunnel: Tunnel | null; onClose: () => void }) {
  const [lines, setLines] = useState<string[]>([]);
  useEffect(() => {
    if (!tunnel) return;
    setLines([
      `[${new Date().toLocaleTimeString()}] 隧道 ${tunnel.name} (${tunnel.id})`,
      `协议 ${tunnel.proto.toUpperCase()} → ${tunnel.local_ip}:${tunnel.local_port}`,
      `公网地址 ${tunnel.address}`,
      `状态 ${tunnel.active ? '运行中' : '已停止'}`,
      '客户端转发日志请查看「运行日志」面板。',
    ]);
  }, [tunnel]);
  return (
    <Modal open={Boolean(tunnel)} title="隧道信息" onClose={onClose} footer={<Button variant="ghost" onClick={onClose}>关闭</Button>}>
      <pre className="mono-block">{lines.join('\n')}</pre>
    </Modal>
  );
}
