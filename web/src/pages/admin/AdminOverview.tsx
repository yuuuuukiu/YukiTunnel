import { useQuery } from '@tanstack/react-query';
import { Link } from 'react-router-dom';
import { Activity, FileClock, Globe2, Network, ShieldCheck, Users } from 'lucide-react';
import { api } from '../../lib/api';
import { Badge, Card, CardHeader, Spinner } from '../../components/ui';
import { formatTime } from '../../lib/format';

export default function AdminOverviewPage() {
  const q = useQuery({ queryKey: ['overview'], queryFn: api.overview, refetchInterval: 10000 });
  if (q.isLoading) return <Spinner label="加载管理概览…" />;
  if (!q.data) return <Card><p>无法加载管理数据。</p></Card>;
  const { counts, settings, recent_audit } = q.data;

  return (
    <>
      <div className="page-head">
        <div>
          <h1>管理概览</h1>
          <p>服务端版本 {q.data.version} · API {settings.api_addr}</p>
        </div>
        <Badge tone={settings.domain_binding ? 'good' : 'neutral'}>
          域名绑定 {settings.domain_binding ? '已开启' : '已关闭'}
        </Badge>
      </div>

      <div className="grid grid-4">
        <StatCard icon={<Users size={17} />} label="用户" value={counts.users} to="/admin/users" />
        <StatCard icon={<Network size={17} />} label="节点（在线）" value={`${counts.nodes_online}/${counts.nodes}`} to="/admin/nodes" />
        <StatCard icon={<Activity size={17} />} label="隧道（运行中）" value={`${counts.tunnels_active}/${counts.tunnels}`} to="/admin/tunnels" />
        <StatCard icon={<Globe2 size={17} />} label="公网端口范围" value={`${settings.port_min}-${settings.port_max}`} to="/admin/settings" />
      </div>

      <div className="grid grid-2">
        <Card>
          <CardHeader title="运行时设置" subtitle="修改后立即生效" actions={<Link to="/admin/settings" className="btn btn-ghost btn-sm"><ShieldCheck size={13} /> 修改</Link>} />
          <dl className="kv">
            <dt>域名绑定</dt><dd>{settings.domain_binding ? <Badge tone="good">开启</Badge> : <Badge tone="neutral">关闭</Badge>}</dd>
            <dt>域名后缀</dt><dd className="mono">{settings.domain_suffix || '—'}</dd>
            <dt>公网入口</dt><dd className="mono">{settings.public_host || '自动探测'}</dd>
            <dt>端口范围</dt><dd className="mono">{settings.port_min} - {settings.port_max}</dd>
            <dt>控制监听</dt><dd className="mono">{settings.control_addr}</dd>
          </dl>
        </Card>
        <Card>
          <CardHeader title="最近审计" subtitle="所有管理操作都会记录" actions={<Link to="/admin/audit" className="btn btn-ghost btn-sm"><FileClock size={13} /> 全部</Link>} />
          <div className="hint-list">
            {recent_audit.length === 0 && <p className="faint small">暂无记录</p>}
            {recent_audit.slice(0, 8).map((a) => (
              <div key={a.id} className="row gap-8 small">
                <span className="faint" style={{ width: 132 }}>{formatTime(a.created_at)}</span>
                <strong>{a.actor_name || '系统'}</strong>
                <span className="muted">{a.action}</span>
                <span className="faint grow" style={{ overflow: 'hidden', textOverflow: 'ellipsis', whiteSpace: 'nowrap' }}>{a.detail}</span>
              </div>
            ))}
          </div>
        </Card>
      </div>
    </>
  );
}

function StatCard({ icon, label, value, to }: { icon: React.ReactNode; label: string; value: React.ReactNode; to: string }) {
  return (
    <Link to={to} className="card" style={{ display: 'block' }}>
      <div className="row gap-8 faint small">{icon}{label}</div>
      <div className="stat-value" style={{ marginTop: 8 }}>{value}</div>
    </Link>
  );
}
