import { useState } from 'react';
import { KeyRound, Loader2, LockKeyhole, Server, ShieldCheck, Ticket } from 'lucide-react';
import { useAuth } from '../state/auth';
import { Button, Field } from '../components/ui';
import { ApiError } from '../lib/api';

type Mode = 'password' | 'token' | 'key';

export default function LoginPage() {
  const { login, loginWithKey, loginWithToken } = useAuth();
  const [mode, setMode] = useState<Mode>('password');
  const [username, setUsername] = useState('');
  const [password, setPassword] = useState('');
  const [token, setToken] = useState('');
  const [privateKey, setPrivateKey] = useState('');
  const [error, setError] = useState('');
  const [busy, setBusy] = useState(false);

  const submit = async (e: React.FormEvent) => {
    e.preventDefault();
    setError('');
    setBusy(true);
    try {
      if (mode === 'password') {
        if (!username || !password) throw new Error('请输入用户名和密码');
        await login(username.trim(), password);
      } else if (mode === 'token') {
        if (!token.trim()) throw new Error('请输入 API Token 或管理令牌');
        await loginWithToken(token.trim());
      } else {
        if (!username || !privateKey.trim()) throw new Error('请输入用户名并粘贴 RSA 私钥');
        await loginWithKey(username.trim(), privateKey);
      }
    } catch (err) {
      if (err instanceof ApiError) setError(err.message);
      else if (err instanceof Error) setError(err.message);
      else setError('登录失败，请重试');
    } finally {
      setBusy(false);
    }
  };

  const onKeyFile = async (file: File | undefined) => {
    if (!file) return;
    setPrivateKey(await file.text());
  };

  return (
    <div className="auth-page">
      <div className="auth-hero">
        <div className="brand">
          <div className="brand-mark">Y</div>
          <div className="brand-text">
            <strong>YukiTunnel</strong>
            <span>内网穿透管理平台</span>
          </div>
        </div>
        <h1>
          把内网服务
          <br />
          安全地带到公网
        </h1>
        <p>无需编辑任何配置文件。选择节点、填写端口，一键创建并启动隧道。</p>
        <ul>
          <li>支持管理 Token 免密码登录（Docker 日志开箱即见）</li>
          <li>多节点 / 多公网入口，实时延迟可见</li>
          <li>TCP · UDP · HTTP · HTTPS 全协议</li>
          <li>密码与 RSA Key 双认证，全程 TLS</li>
        </ul>
      </div>
      <div className="auth-panel">
        <form className="auth-card" onSubmit={submit}>
          <div>
            <h2>管理员登录</h2>
            <p className="small">使用账号密码或专属管理 Token 快速进入管理控制台。</p>
          </div>
          <div className="segmented" role="tablist">
            <button type="button" role="tab" className={mode === 'password' ? 'active' : ''} onClick={() => setMode('password')}>
              <LockKeyhole size={14} style={{ marginRight: 6, verticalAlign: -2 }} />
              密码登录
            </button>
            <button type="button" role="tab" className={mode === 'token' ? 'active' : ''} onClick={() => setMode('token')}>
              <Ticket size={14} style={{ marginRight: 6, verticalAlign: -2 }} />
              管理 Token
            </button>
            <button type="button" role="tab" className={mode === 'key' ? 'active' : ''} onClick={() => setMode('key')}>
              <KeyRound size={14} style={{ marginRight: 6, verticalAlign: -2 }} />
              RSA Key
            </button>
          </div>
          {mode !== 'token' && (
            <Field label="用户名">
              <input value={username} onChange={(e) => setUsername(e.target.value)} autoComplete="username" placeholder="admin" autoFocus />
            </Field>
          )}
          {mode === 'password' && (
            <Field label="密码">
              <input
                type="password"
                value={password}
                onChange={(e) => setPassword(e.target.value)}
                autoComplete="current-password"
                placeholder="••••••••"
              />
            </Field>
          )}
          {mode === 'token' && (
            <Field label="管理 Token" hint="粘贴从 docker logs yuki-server 获取的 yk_... 令牌">
              <input
                type="password"
                value={token}
                onChange={(e) => setToken(e.target.value)}
                placeholder="yk_..."
                autoComplete="off"
                autoFocus
              />
            </Field>
          )}
          {mode === 'key' && (
            <Field label="RSA 私钥" hint="支持 PEM（PKCS#1 / PKCS#8）。私钥只在本地浏览器中用于签名，不会上传。">
              <textarea
                value={privateKey}
                onChange={(e) => setPrivateKey(e.target.value)}
                placeholder={'-----BEGIN RSA PRIVATE KEY-----\n...'}
                rows={6}
              />
              <input
                type="file"
                accept=".pem,.key,text/plain"
                style={{ marginTop: 6 }}
                onChange={(e) => onKeyFile(e.target.files?.[0])}
              />
            </Field>
          )}
          {error && (
            <div className="alert alert-bad" role="alert">
              {error}
            </div>
          )}
          <Button type="submit" disabled={busy}>
            {busy ? <Loader2 size={16} className="spinner-icon" /> : <ShieldCheck size={16} />}
            {busy ? '正在验证…' : mode === 'token' ? '进入管理台' : '登录'}
          </Button>
          <div className="hint-list">
            <span className="row gap-8">
              <Server size={14} /> 当前服务端：<span className="mono">{window.location.origin}</span>
            </span>
          </div>
        </form>
      </div>
    </div>
  );
}
