import { useEffect, useRef, useState } from 'react';
import { Pause, Play, Radio, ScrollText, Terminal } from 'lucide-react';
import { useAuth } from '@web/state/auth';
import { useToast } from '@web/state/toast';
import { Badge, Button, Card, CardHeader, Modal } from '@web/components/ui';
import { desktop, isDesktop, listen, type AgentStatus } from '../lib/tauri';
import { readInsecure } from '../lib/transport';

/**
 * The agent is the piece that actually moves bytes. The GUI keeps it running
 * in the background (tray) and shows its live output here.
 */
export function AgentControl({ tunnelCount, activeCount }: { tunnelCount: number; activeCount: number }) {
  const { serverUrl } = useAuth();
  const toast = useToast();
  const [status, setStatus] = useState<AgentStatus | null>(null);
  const [logs, setLogs] = useState<string[]>([]);
  const [showLogs, setShowLogs] = useState(false);
  const [busy, setBusy] = useState(false);
  const logRef = useRef<HTMLPreElement>(null);

  useEffect(() => {
    if (!isDesktop) return;
    void desktop.agentStatus().then(setStatus).catch(() => {});
    let unlistenLog: (() => void) | undefined;
    let unlistenState: (() => void) | undefined;
    void listen<{ kind: string; message: string }>('agent://log', (p) => {
      setLogs((prev) => [...prev.slice(-499), p.message]);
    }).then((fn) => (unlistenLog = fn));
    void listen<{ kind: string; message: string }>('agent://state', (p) => {
      setLogs((prev) => [...prev.slice(-499), p.message]);
      void desktop.agentStatus().then(setStatus).catch(() => {});
    }).then((fn) => (unlistenState = fn));
    return () => {
      unlistenLog?.();
      unlistenState?.();
    };
  }, []);

  useEffect(() => {
    logRef.current?.scrollTo({ top: logRef.current.scrollHeight });
  }, [logs]);

  const start = async () => {
    if (!serverUrl) return;
    setBusy(true);
    try {
      const res = await desktop.agentStart({ serverUrl, autoReconnect: true, insecure: readInsecure() });
      setStatus(res);
      toast.success('穿透已启动', '断线会自动重连，关闭窗口后仍在托盘运行');
    } catch (e) {
      toast.error('启动失败', e instanceof Error ? e.message : '');
    } finally {
      setBusy(false);
    }
  };

  const stop = async () => {
    setBusy(true);
    try {
      setStatus(await desktop.agentStop());
      toast.info('穿透已停止');
    } catch (e) {
      toast.error('停止失败', e instanceof Error ? e.message : '');
    } finally {
      setBusy(false);
    }
  };

  const running = status?.running ?? false;

  return (
    <Card>
      <CardHeader
        title="穿透运行状态"
        subtitle={isDesktop ? '保持客户端运行即可持续转发；关闭窗口会最小化到托盘' : '浏览器预览模式不会启动本地转发进程'}
        actions={
          <>
            <Button size="sm" variant="ghost" onClick={() => setShowLogs(true)}><ScrollText size={13} /> 运行日志</Button>
            {running ? (
              <Button size="sm" variant="subtle" onClick={stop} disabled={busy}><Pause size={13} /> 停止穿透</Button>
            ) : (
              <Button size="sm" onClick={start} disabled={busy || !isDesktop}><Play size={13} /> 启动穿透</Button>
            )}
          </>
        }
      />
      <div className="row gap-16 wrap">
        <div className="row gap-8">
          {running ? <Badge tone="good"><Radio size={12} /> 客户端已连接</Badge> : <Badge tone="neutral">客户端未运行</Badge>}
          {status?.pid && <span className="faint small mono">PID {status.pid}</span>}
        </div>
        <div className="small muted">隧道 {activeCount} / {tunnelCount} 运行中</div>
        <span className="grow" />
        <div className="small faint row gap-8"><Terminal size={13} /> 自动重连 {running ? '已启用' : '—'}</div>
      </div>
      {!isDesktop && (
        <div className="alert alert-info" style={{ marginTop: 12 }}>
          当前是浏览器预览。请使用桌面客户端（或 CLI <span className="mono">yuki run</span>）建立实际转发。
        </div>
      )}

      <Modal
        open={showLogs}
        title="客户端运行日志"
        onClose={() => setShowLogs(false)}
        width={760}
        footer={<Button variant="ghost" onClick={() => setShowLogs(false)}>关闭</Button>}
      >
        <pre className="mono-block" ref={logRef} style={{ maxHeight: 380 }}>
          {logs.length ? logs.join('\n') : '暂无日志。启动穿透后会在这里实时显示。'}
        </pre>
      </Modal>
    </Card>
  );
}
