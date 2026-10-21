import { useQuery } from '@tanstack/react-query';
import { Link } from 'react-router-dom';
import { Activity, Network, Plus, RefreshCw } from 'lucide-react';
import { api } from '../lib/api';
import { Badge, Button, Card, EmptyState, Spinner } from '../components/ui';
import { latencyClass } from '../lib/format';

export default function ServersPage() {
  const query = useQuery({ queryKey: ['servers'], queryFn: api.servers, refetchInterval: 10000 });

  return (
    <>
      <div className="page-head">
        <div>
          <h1>可用节点</h1>
          <p>节点是公网入口。延迟为服务端到节点控制端口的 TCP 探测值，每 10 秒刷新。</p>
        </div>
        <div className="row gap-8">
          <Button variant="subtle" onClick={() => query.refetch()} disabled={query.isFetching}>
            <RefreshCw size={15} className={query.isFetching ? 'spinner-icon' : ''} /> 刷新
          </Button>
          <Link to="/tunnels?create=1" className="btn btn-primary btn-md"><Plus size={15} /> 新建隧道</Link>
        </div>
      </div>

      {query.isLoading ? (
        <Spinner label="探测节点…" />
      ) : (query.data?.servers.length ?? 0) === 0 ? (
        <Card>
          <EmptyState icon={<Network size={30} />} title="暂无节点" detail="请联系管理员在「节点管理」中添加公网入口。" />
        </Card>
      ) : (
        <div className="grid grid-2">
          {query.data!.servers.map((s) => (
            <Card key={s.id}>
              <div className="row" style={{ justifyContent: 'space-between', alignItems: 'flex-start' }}>
                <div>
                  <div className="row gap-8">
                    <h2>{s.name}</h2>
                    {s.online ? <Badge tone="good"><span className="dot dot-good" /> 在线</Badge> : <Badge tone="bad">离线</Badge>}
                  </div>
                  <p className="small" style={{ marginTop: 4 }}>
                    {s.region || '默认区域'} · {s.public_host} · 端口 {s.port_range[0]}-{s.port_range[1]}
                  </p>
                </div>
                <div className="right">
                  <div className={`latency latency-${latencyClass(s.latency_ms)}`} style={{ fontSize: 20, fontWeight: 700 }}>
                    {s.latency_ms || '—'}<span style={{ fontSize: 12 }}> ms</span>
                  </div>
                  <div className="faint small">延迟</div>
                </div>
              </div>
              <div className="divider" style={{ margin: '12px 0' }} />
              <div className="row gap-16 small muted wrap">
                <span className="row gap-8"><Activity size={13} /> 隧道 {s.tunnel_count}{s.max_tunnels ? ` / ${s.max_tunnels}` : ''}</span>
                <span className="row gap-8"><Network size={13} /> ID <span className="mono">{s.id}</span></span>
                {s.domain && <span className="row gap-8">域名 <span className="mono">{s.domain}</span></span>}
                <span className="grow" />
                <Link to={`/tunnels?create=1&node=${s.id}`} className="btn btn-ghost btn-sm">用此节点创建</Link>
              </div>
            </Card>
          ))}
        </div>
      )}
    </>
  );
}
