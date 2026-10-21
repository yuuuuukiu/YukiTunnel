import { useEffect, useState } from 'react';
import { FolderCog, Info, RefreshCw } from 'lucide-react';
import { useAuth } from '@web/state/auth';
import { Button, Card, CardHeader, Field } from '@web/components/ui';
import { desktop, isDesktop, type PathsInfo } from '../lib/tauri';

export default function SettingsPage() {
  const { serverUrl, setServerUrl } = useAuth();
  const [url, setUrl] = useState(serverUrl ?? '');
  const [paths, setPaths] = useState<PathsInfo | null>(null);
  const [notes, setNotes] = useState<{ firewall_hint: string; docker_alternative: string } | null>(null);

  useEffect(() => {
    if (!isDesktop) return;
    void desktop.paths().then(setPaths).catch(() => {});
    void desktop.installNotes().then(setNotes).catch(() => {});
  }, []);

  return (
    <>
      <div className="page-head">
        <div>
          <h1>设置</h1>
          <p>连接信息保存在本机配置目录，不会上传到任何第三方。</p>
        </div>
      </div>
      <div className="grid grid-2">
        <Card>
          <CardHeader title="服务端连接" subtitle="切换服务端后需要重新登录" />
          <Field label="服务端地址">
            <input value={url} onChange={(e) => setUrl(e.target.value)} placeholder="https://panel.example.com:8443" />
          </Field>
          <div className="row gap-8" style={{ marginTop: 12 }}>
            <Button size="sm" onClick={() => setServerUrl(url.trim() || null)}>保存地址</Button>
            <Button size="sm" variant="ghost" onClick={() => { setUrl(serverUrl ?? ''); }}><RefreshCw size={13} /> 还原</Button>
          </div>
        </Card>
        <Card>
          <CardHeader title="本地文件" subtitle="用于排查问题或迁移数据" />
          <dl className="kv">
            <dt>配置目录</dt><dd className="mono small"><PathText value={paths?.config_dir} /></dd>
            <dt>默认数据目录</dt><dd className="mono small"><PathText value={paths?.data_dir} /></dd>
            <dt>服务端组件</dt><dd className="mono small"><PathText value={paths?.server_binary} fallback="未找到" /></dd>
            <dt>客户端组件</dt><dd className="mono small"><PathText value={paths?.client_binary} fallback="未找到" /></dd>
          </dl>
        </Card>
      </div>
      {notes && (
        <Card>
          <CardHeader title="部署提示" subtitle="按平台整理的网络与防火墙注意事项" />
          <div className="hint-list">
            <div className="row gap-8"><FolderCog size={14} /> {notes.firewall_hint}</div>
            <div className="row gap-8"><Info size={14} /> {notes.docker_alternative}</div>
          </div>
        </Card>
      )}
    </>
  );
}

/**
 * PathText renders a filesystem path so it wraps at separators.
 *
 * Browsers only break at spaces and hyphens, so an absolute path either
 * overflows the card or gets split mid-word ("yuki-serv/er"). Inserting a
 * zero-width space after each separator gives the layout engine a sane place
 * to break without changing what is copied out.
 */
function PathText({ value, fallback = '—' }: { value?: string | null; fallback?: string }) {
  if (!value) return <>{fallback}</>;
  const parts = value.split('/');
  return (
    <>
      {parts.map((part, i) => (
        <span key={i}>
          {part}
          {i < parts.length - 1 && (
            <>
              /<wbr />
            </>
          )}
        </span>
      ))}
    </>
  );
}
