import { useEffect, useMemo, useState } from 'react';
import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query';
import { useNavigate, useSearchParams } from 'react-router-dom';
import {
  ArrowDownToLine, ArrowUpFromLine, Copy, Globe2, Loader2, Pencil, Play, Plus, Square, Trash2,
} from 'lucide-react';
import { api, ApiError } from '../lib/api';
import type { Tunnel } from '../lib/types';
import { useToast } from '../state/toast';
import { Badge, Button, Card, EmptyState, Modal, Spinner } from '../components/ui';
import { CreateTunnelModal } from '../components/CreateTunnelModal';
import { copyText, formatTime, humanBytes, timeAgo } from '../lib/format';

export default function TunnelsPage() {
  const qc = useQueryClient();
  const toast = useToast();
  const [params, setParams] = useSearchParams();
  const navigate = useNavigate();
  const [creating, setCreating] = useState(params.get('create') === '1');
  const [editing, setEditing] = useState<Tunnel | null>(null);
  const [deleting, setDeleting] = useState<Tunnel | null>(null);
  const [busyId, setBusyId] = useState<string | null>(null);

  const query = useQuery({ queryKey: ['tunnels'], queryFn: api.tunnels, refetchInterval: 4000 });

  useEffect(() => {
    if (params.get('create') === '1') {
      setCreating(true);
      params.delete('create');
      setParams(params, { replace: true });
    }
  }, [params, setParams]);

  const toggle = useMutation({
    mutationFn: async (t: Tunnel) => {
      setBusyId(t.id);
      return t.active ? api.stopTunnel(t.id) : api.startTunnel(t.id);
    },
    onSuccess: (_res, t) => {
      void qc.invalidateQueries({ queryKey: ['tunnels'] });
      toast.success(t.active ? '隧道已停止' : '隧道已启动', t.address);
    },
    onError: (err, t) => {
      toast.error(t.active ? '停止失败' : '启动失败', err instanceof ApiError || err instanceof Error ? err.message : '未知错误');
    },
    onSettled: () => setBusyId(null),
  });

  const remove = useMutation({
    mutationFn: (t: Tunnel) => api.deleteTunnel(t.id),
    onSuccess: () => {
      void qc.invalidateQueries({ queryKey: ['tunnels'] });
      toast.success('隧道已删除');
      setDeleting(null);
    },
    onError: (err) => toast.error('删除失败', err instanceof Error ? err.message : '未知错误'),
  });

  const list = useMemo(() => query.data?.tunnels ?? [], [query.data]);

  return (
    <>
      <div className="page-head">
        <div>
          <h1>我的隧道</h1>
          <p>启动后公网地址立即生效；关闭客户端后服务端会保留配置，下次连接自动恢复。</p>
        </div>
        <Button onClick={() => setCreating(true)}>
          <Plus size={16} /> 新建隧道
        </Button>
      </div>

      {query.isLoading ? (
        <Spinner label="加载隧道…" />
      ) : list.length === 0 ? (
        <Card>
          <EmptyState
            icon={<Globe2 size={30} />}
            title="还没有隧道"
            detail="选择节点、填写内网 IP 与端口，几秒钟即可把内网服务暴露到公网。"
            action={<Button onClick={() => setCreating(true)}><Plus size={15} /> 创建第一条隧道</Button>}
          />
        </Card>
      ) : (
        <div className="grid grid-2">
          {list.map((t) => (
            <Card key={t.id} className="tunnel-card">
              <div className="tunnel-card-top">
                <div>
                  <div className="row gap-8">
                    <h2>{t.name}</h2>
                    <Badge tone="neutral">{t.proto.toUpperCase()}</Badge>
                    {!t.enabled && <Badge tone="warn">已禁用</Badge>}
                  </div>
                  <p className="small" style={{ marginTop: 4 }}>
                    节点 {t.node_name} · {t.local_ip}:{t.local_port} · 创建于 {timeAgo(t.created_at)}
                  </p>
                </div>
                {t.active ? <Badge tone="good"><span className="dot dot-good" /> 运行中</Badge> : <Badge tone="neutral">已停止</Badge>}
              </div>

              <div className="tunnel-address">
                <Globe2 size={14} className="faint" />
                <span className="mono">{t.address}</span>
                <Button
                  size="sm"
                  variant="ghost"
                  onClick={() => {
                    void copyText(t.address);
                    toast.success('已复制访问地址');
                  }}
                >
                  <Copy size={12} /> 复制
                </Button>
              </div>

              <div className="row gap-16 small muted wrap">
                <span className="row gap-8"><ArrowUpFromLine size={13} /> 上行 {humanBytes(t.rx_bytes)}</span>
                <span className="row gap-8"><ArrowDownToLine size={13} /> 下行 {humanBytes(t.tx_bytes)}</span>
                <span className="row gap-8"><span className="dot dot-good" /> 连接 {t.active_connections}</span>
                <span className="grow" />
                <span className="faint">最近启动 {timeAgo(t.last_started_at || t.created_at)}</span>
              </div>

              <div className="row gap-8 wrap">
                <Button
                  variant={t.active ? 'subtle' : 'primary'}
                  size="sm"
                  disabled={busyId === t.id || !t.enabled}
                  onClick={() => toggle.mutate(t)}
                >
                  {busyId === t.id ? <Loader2 size={13} className="spinner-icon" /> : t.active ? <Square size={13} /> : <Play size={13} />}
                  {t.active ? '停止' : '启动'}
                </Button>
                <Button size="sm" variant="ghost" onClick={() => setEditing(t)}>
                  <Pencil size={13} /> 编辑
                </Button>
                <Button size="sm" variant="ghost" onClick={() => navigate(`/tunnels/${t.id}`)}>
                  详情
                </Button>
                <span className="grow" />
                <Button size="sm" variant="danger" onClick={() => setDeleting(t)}>
                  <Trash2 size={13} /> 删除
                </Button>
              </div>
            </Card>
          ))}
        </div>
      )}

      <CreateTunnelModal open={creating} onClose={() => setCreating(false)} />
      <CreateTunnelModal open={Boolean(editing)} initial={editing} onClose={() => setEditing(null)} />
      <Modal
        open={Boolean(deleting)}
        title="删除隧道"
        onClose={() => setDeleting(null)}
        footer={
          <>
            <Button variant="ghost" onClick={() => setDeleting(null)}>取消</Button>
            <Button variant="danger" disabled={remove.isPending} onClick={() => deleting && remove.mutate(deleting)}>
              确认删除
            </Button>
          </>
        }
      >
        <p>
          确定删除隧道 <strong>{deleting?.name}</strong>（{deleting?.address}）吗？删除后公网地址立即失效，且无法恢复。
        </p>
        <p className="small faint">创建时间：{formatTime(deleting?.created_at ?? '')}</p>
      </Modal>
    </>
  );
}
