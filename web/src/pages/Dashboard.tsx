import { useQuery } from '@tanstack/react-query';
import { Link } from 'react-router-dom';
import { Activity, ArrowUpRight, Globe2, Network, Plus, Users } from 'lucide-react';
import { api } from '../lib/api';
import { useAuth } from '../state/auth';
import { Badge, Card, CardHeader, EmptyState, Spinner } from '../components/ui';
import { formatTime, humanBytes, latencyClass, timeAgo } from '../lib/format';

export default function DashboardPage() {
  const { user } = useAuth();
  const isAdmin = user?.role === 'admin';

  const tunnels = useQuery({ queryKey: ['tunnels'], queryFn: api.tunnels, refetchInterval: 5000 });
  const servers = useQuery({ queryKey: ['servers'], queryFn: api.servers, refetchInterval: 15000 });
  const overview = useQuery({ queryKey: ['overview'], queryFn: api.overview, enabled: isAdmin, refetchInterval: 10000 });

  const list = tunnels.data?.tunnels ?? [];
  const active = list.filter((t) => t.active).length;
  const traffic = list.reduce((sum, t) => sum + t.rx_bytes + t.tx_bytes, 0);

  return (
    <>
      <div className="page-head">
        <div>
          <h1>你好，{user?.display_name || user?.username}</h1>
          <p>这里可以看到节点状态、隧道运行情况和实时流量。</p>
        </div>
        <Link to="/tunnels?create=1" className="btn btn-primary btn-md">
          <Plus size={16} /> 新建隧道
        </Link>
      </div>

      <div className="grid grid-4">
        <Card>
          <div className="stat">
            <span className="stat-label">在线节点</span>
            <span className="stat-value">
              {servers.data ? servers.data.servers.filter((s) => s.online).length : '—'}
              <span className="faint" style={{ fontSize: 15 }}> / {servers.data?.servers.length ?? '—'}</span>
            </span>
          </div>
        </Card>
        <Card>
          <div className="stat">
            <span className="stat-label">运行中隧道</span>
            <span className="stat-value">
              {active}
              <span className="faint" style={{ fontSize: 15 }}> / {list.length}</span>
            </span>
          </div>
        </Card>
        <Card>
          <div className="stat">
            <span className="stat-label">累计流量</span>
            <span className="stat-value">{humanBytes(traffic)}</span>
          </div>
        </Card>
        <Card>
          <div className="stat">
            <span className="stat-label">{isAdmin ? '用户总数' : '域名绑定'}</span>
            <span className="stat-value" style={{ fontSize: isAdmin ? 26 : 18 }}>
              {isAdmin ? overview.data?.counts.users ?? '—' : servers.data?.domain_binding ? '已开启' : '已关闭'}
            </span>
          </div>
        </Card>
      </div>

      <div className="grid grid-2">
        <Card>
          <CardHeader
            title="节点"
            subtitle="延迟为服务端到节点控制端口的探测值"
            actions={<Link to="/servers" className="btn btn-ghost btn-sm">全部 <ArrowUpRight size={13} /></Link>}
          />
          {servers.isLoading ? (
            <Spinner label="加载节点…" />
          ) : (servers.data?.servers.length ?? 0) === 0 ? (
            <EmptyState icon={<Network size={26} />} title="还没有节点" detail="请联系管理员在管理端添加公网入口。" />
          ) : (
            <div className="table-wrap">
              <table>
                <thead>
                  <tr>
                    <th>节点</th>
                    <th>公网入口</th>
                    <th>延迟</th>
                    <th>状态</th>
                  </tr>
                </thead>
                <tbody>
                  {servers.data!.servers.slice(0, 5).map((s) => (
                    <tr key={s.id}>
                      <td>
                        <strong>{s.name}</strong>
                        <div className="faint small">{s.region || '—'}</div>
                      </td>
                      <td className="mono small">{s.public_host}</td>
                      <td>
                        <span className={`latency latency-${latencyClass(s.latency_ms)}`}>{s.latency_ms} ms</span>
                      </td>
                      <td>
                        {s.online ? <Badge tone="good">在线</Badge> : <Badge tone="bad">离线</Badge>}
                      </td>
                    </tr>
                  ))}
                </tbody>
              </table>
            </div>
          )}
        </Card>

        <Card>
          <CardHeader
            title="最近隧道"
            subtitle="点击隧道进入详情，可启动 / 停止"
            actions={<Link to="/tunnels" className="btn btn-ghost btn-sm">全部 <ArrowUpRight size={13} /></Link>}
          />
          {tunnels.isLoading ? (
            <Spinner label="加载隧道…" />
          ) : list.length === 0 ? (
            <EmptyState
              icon={<Globe2 size={26} />}
              title="还没有隧道"
              detail="点击「新建隧道」，选择节点并填写内网地址即可。"
              action={<Link to="/tunnels?create=1" className="btn btn-primary btn-sm"><Plus size={14} /> 新建隧道</Link>}
            />
          ) : (
            <div className="table-wrap">
              <table>
                <thead>
                  <tr>
                    <th>名称</th>
                    <th>公网地址</th>
                    <th>状态</th>
                    <th className="right">流量</th>
                  </tr>
                </thead>
                <tbody>
                  {list.slice(0, 5).map((t) => (
                    <tr key={t.id}>
                      <td>
                        <Link to={`/tunnels/${t.id}`}>{t.name}</Link>
                        <div className="faint small">{t.proto.toUpperCase()}</div>
                      </td>
                      <td className="mono small">{t.address}</td>
                      <td>{t.active ? <Badge tone="good">运行中</Badge> : <Badge tone="neutral">已停止</Badge>}</td>
                      <td className="right mono small">{humanBytes(t.rx_bytes + t.tx_bytes)}</td>
                    </tr>
                  ))}
                </tbody>
              </table>
            </div>
          )}
        </Card>
      </div>

      {isAdmin && overview.data && (
        <Card>
          <CardHeader title="管理概览" subtitle="最近的关键操作" actions={<Link to="/admin" className="btn btn-ghost btn-sm">进入管理 <ArrowUpRight size={13} /></Link>} />
          <div className="grid grid-4" style={{ marginBottom: 14 }}>
            <MiniStat icon={<Users size={16} />} label="用户" value={overview.data.counts.users} />
            <MiniStat icon={<Network size={16} />} label="在线节点" value={`${overview.data.counts.nodes_online}/${overview.data.counts.nodes}`} />
            <MiniStat icon={<Activity size={16} />} label="运行隧道" value={`${overview.data.counts.tunnels_active}/${overview.data.counts.tunnels}`} />
            <MiniStat icon={<Globe2 size={16} />} label="域名绑定" value={overview.data.settings.domain_binding ? '开启' : '关闭'} />
          </div>
          <div className="hint-list">
            {overview.data.recent_audit.slice(0, 5).map((a) => (
              <div key={a.id} className="row gap-8 small">
                <span className="faint" style={{ width: 140 }}>{formatTime(a.created_at)}</span>
                <strong>{a.actor_name || '系统'}</strong>
                <span className="muted">{a.action}</span>
                <span className="faint">{a.detail}</span>
                <span className="grow" />
                <span className="faint">{timeAgo(a.created_at)}</span>
              </div>
            ))}
          </div>
        </Card>
      )}
    </>
  );
}

function MiniStat({ icon, label, value }: { icon: React.ReactNode; label: string; value: React.ReactNode }) {
  return (
    <div className="row gap-12" style={{ padding: '10px 12px', borderRadius: 10, background: 'rgba(9,14,28,0.4)', border: '1px solid var(--line-soft)' }}>
      <span className="faint">{icon}</span>
      <div>
        <div style={{ fontWeight: 700 }}>{value}</div>
        <div className="faint small">{label}</div>
      </div>
    </div>
  );
}
