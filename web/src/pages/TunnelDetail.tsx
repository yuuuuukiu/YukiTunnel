import { useQuery } from '@tanstack/react-query';
import { Link, useNavigate, useParams } from 'react-router-dom';
import { ArrowLeft, Copy, Play, Square } from 'lucide-react';
import { api } from '../lib/api';
import { useToast } from '../state/toast';
import { Badge, Button, Card, CardHeader, Spinner } from '../components/ui';
import { copyText, formatTime, humanBytes } from '../lib/format';
import { useQueryClient } from '@tanstack/react-query';

export default function TunnelDetailPage() {
  const { id = '' } = useParams();
  const navigate = useNavigate();
  const toast = useToast();
  const qc = useQueryClient();

  const tunnels = useQuery({ queryKey: ['tunnels'], queryFn: api.tunnels, refetchInterval: 3000 });
  const stats = useQuery({ queryKey: ['tunnel-stats', id], queryFn: () => api.tunnelStats(id), refetchInterval: 3000, enabled: Boolean(id) });
  const t = tunnels.data?.tunnels.find((x) => x.id === id);

  if (tunnels.isLoading) return <Spinner label="加载…" />;
  if (!t) {
    return (
      <Card>
        <p>隧道不存在或已被删除。</p>
        <Link to="/tunnels" className="btn btn-subtle btn-sm" style={{ marginTop: 12 }}>返回列表</Link>
      </Card>
    );
  }

  const action = async (start: boolean) => {
    try {
      if (start) await api.startTunnel(t.id);
      else await api.stopTunnel(t.id);
      void qc.invalidateQueries({ queryKey: ['tunnels'] });
      toast.success(start ? '隧道已启动' : '隧道已停止');
    } catch (e) {
      toast.error('操作失败', e instanceof Error ? e.message : '');
    }
  };

  return (
    <>
      <div className="page-head">
        <div>
          <button className="nav-item" style={{ width: 'auto', padding: 0, color: 'var(--text-dim)' }} onClick={() => navigate('/tunnels')}>
            <ArrowLeft size={15} /> 返回隧道列表
          </button>
          <h1 style={{ marginTop: 8 }}>{t.name}</h1>
          <p>{t.proto.toUpperCase()} · 节点 {t.node_name} · {t.local_ip}:{t.local_port}</p>
        </div>
        <div className="row gap-8">
          <Button variant="subtle" onClick={() => action(!t.active)}>
            {t.active ? <Square size={15} /> : <Play size={15} />}
            {t.active ? '停止' : '启动'}
          </Button>
        </div>
      </div>

      <div className="grid grid-2">
        <Card>
          <CardHeader title="公网访问地址" subtitle="可直接分享给外部访问者" />
          <div className="tunnel-address">
            <span className="mono" style={{ fontSize: 16 }}>{t.address}</span>
            <Button
              size="sm"
              variant="ghost"
              onClick={() => {
                void copyText(t.address);
                toast.success('已复制');
              }}
            >
              <Copy size={12} /> 复制
            </Button>
          </div>
          <dl className="kv" style={{ marginTop: 16 }}>
            <dt>状态</dt>
            <dd>{t.active ? <Badge tone="good">运行中</Badge> : <Badge tone="neutral">已停止</Badge>}</dd>
            <dt>地址形式</dt>
            <dd>{t.domain ? <Badge tone="info">域名 + 端口</Badge> : <Badge tone="neutral">IP + 端口</Badge>}</dd>
            <dt>域名</dt>
            <dd className="mono">{t.domain || '—'}</dd>
            <dt>公网端口</dt>
            <dd className="mono">{t.public_port}</dd>
            <dt>创建时间</dt>
            <dd>{formatTime(t.created_at)}</dd>
            <dt>隧道 ID</dt>
            <dd className="mono small">{t.id}</dd>
          </dl>
        </Card>

        <Card>
          <CardHeader title="流量与连接" subtitle="服务端每 5 秒采集一次" />
          <div className="grid grid-2">
            <div className="stat">
              <span className="stat-label">上行（用户 → 内网）</span>
              <span className="stat-value" style={{ fontSize: 20 }}>{humanBytes(stats.data?.rx_bytes ?? t.rx_bytes)}</span>
            </div>
            <div className="stat">
              <span className="stat-label">下行（内网 → 用户）</span>
              <span className="stat-value" style={{ fontSize: 20 }}>{humanBytes(stats.data?.tx_bytes ?? t.tx_bytes)}</span>
            </div>
            <div className="stat">
              <span className="stat-label">累计连接</span>
              <span className="stat-value" style={{ fontSize: 20 }}>{stats.data?.connections ?? t.connections}</span>
            </div>
            <div className="stat">
              <span className="stat-label">当前连接</span>
              <span className="stat-value" style={{ fontSize: 20 }}>{stats.data?.active_connections ?? t.active_connections}</span>
            </div>
          </div>
          <div className="alert alert-info" style={{ marginTop: 16 }}>
            <div>
              <strong>如何验证？</strong>
              <div className="small" style={{ marginTop: 4 }}>
                TCP/UDP：<span className="mono">nc -vz {t.public_host} {t.public_port}</span>
                <br />
                HTTP：<span className="mono">curl http://{t.address}/</span>
              </div>
            </div>
          </div>
        </Card>
      </div>
    </>
  );
}
