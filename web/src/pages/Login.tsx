import { useState } from 'react';
import { KeyRound, Loader2, LockKeyhole, ServerCog, ShieldCheck, Ticket, User } from 'lucide-react';
import { useAuth } from '../state/auth';
import { Button, Field } from '../components/ui';
import { ApiError } from '../lib/api';

/**
 * Two ways in, chosen before any field is shown.
 *
 * Ordinary users sign in with a password or a key. Administrators can instead
 * paste a management token, which is what the server prints on first start and
 * what makes remote administration possible without knowing the password.
 */
export default function LoginPage() {
  const { login, loginWithKey, loginWithToken, remembered, forgetCredentials } = useAuth();
  const [role, setRole] = useState<'user' | 'admin'>(
    remembered.mode === 'token' ? 'admin' : 'user',
  );

  const [username, setUsername] = useState(remembered.username ?? '');
  const [password, setPassword] = useState('');
  const [privateKey, setPrivateKey] = useState('');
  const [credMode, setCredMode] = useState<'password' | 'key'>(
    remembered.mode === 'key' ? 'key' : 'password',
  );
  const [remember, setRemember] = useState(true);

  const [token, setToken] = useState('');

  const [error, setError] = useState('');
  const [busy, setBusy] = useState(false);

  const submitUser = async (e: React.FormEvent) => {
    e.preventDefault();
    setError('');
    setBusy(true);
    try {
      if (!username.trim()) throw new Error('请输入用户名');
      if (credMode === 'password') {
        if (!password) throw new Error('请输入密码');
        await login(username.trim(), password, remember);
      } else {
        if (!privateKey.trim()) throw new Error('请粘贴 RSA 私钥');
        await loginWithKey(username.trim(), privateKey, remember);
      }
    } catch (err) {
      setError(err instanceof ApiError || err instanceof Error ? err.message : '登录失败，请重试');
    } finally {
      setBusy(false);
    }
  };

  const submitAdmin = async (e: React.FormEvent) => {
    e.preventDefault();
    setError('');
    setBusy(true);
    try {
      if (!token.trim()) throw new Error('请输入管理 Token');
      await loginWithToken(token.trim(), remember);
    } catch (err) {
      setError(err instanceof ApiError || err instanceof Error ? err.message : '登录失败，请重试');
    } finally {
      setBusy(false);
    }
  };

  const onKeyFile = async (file: File | undefined) => {
    if (!file) return;
    setPrivateKey(await file.text());
  };

  const switchRole = (next: 'user' | 'admin') => {
    setRole(next);
    setError('');
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
          <li>普通用户：密码或 RSA Key 登录，管理自己的隧道</li>
          <li>管理员：用管理 Token 直接进入管理控制台</li>
          <li>TCP · UDP · HTTP · HTTPS 全协议，全程 TLS</li>
          <li>多节点 / 多公网入口，实时延迟可见</li>
        </ul>
      </div>

      <div className="auth-panel">
        <div className="auth-card">
          <div className="mode-switch">
            <button
              type="button"
              className={`mode-card ${role === 'user' ? 'active' : ''}`}
              onClick={() => switchRole('user')}
            >
              <span className="mode-title"><User size={16} /> 用户登录</span>
              <span className="mode-desc">管理自己的隧道</span>
            </button>
            <button
              type="button"
              className={`mode-card ${role === 'admin' ? 'active' : ''}`}
              onClick={() => switchRole('admin')}
            >
              <span className="mode-title">
                <ServerCog size={16} /> 服务端管理
                <span className="mode-badge">管理员</span>
              </span>
              <span className="mode-desc">用管理 Token 进入</span>
            </button>
          </div>

          {role === 'user' ? (
            <form onSubmit={submitUser} style={{ display: 'flex', flexDirection: 'column', gap: 16 }}>
              <div>
                <h2>用户登录</h2>
                <p className="auth-sub">账号由管理员创建，不开放自助注册。</p>
              </div>
              <Field label="用户名">
                <input
                  value={username}
                  onChange={(e) => setUsername(e.target.value)}
                  autoComplete="username"
                  placeholder="alice"
                  autoFocus
                />
              </Field>
              <div className="segmented" role="tablist">
                <button type="button" role="tab" className={credMode === 'password' ? 'active' : ''} onClick={() => setCredMode('password')}>
                  <LockKeyhole size={14} style={{ marginRight: 6, verticalAlign: -2 }} />
                  密码
                </button>
                <button type="button" role="tab" className={credMode === 'key' ? 'active' : ''} onClick={() => setCredMode('key')}>
                  <KeyRound size={14} style={{ marginRight: 6, verticalAlign: -2 }} />
                  RSA Key
                </button>
              </div>
              {credMode === 'password' ? (
                <Field label="密码">
                  <input
                    type="password"
                    value={password}
                    onChange={(e) => setPassword(e.target.value)}
                    autoComplete="current-password"
                    placeholder="••••••••"
                  />
                </Field>
              ) : (
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
              <label className="checkbox-row">
                <input type="checkbox" checked={remember} onChange={(e) => setRemember(e.target.checked)} />
                记住此设备，下次自动登录
              </label>
              {error && (
                <div className="alert alert-bad" role="alert">
                  {error}
                </div>
              )}
              <Button type="submit" disabled={busy}>
                {busy ? <Loader2 size={16} className="spinner-icon" /> : <ShieldCheck size={16} />}
                {busy ? '正在验证…' : '登录'}
              </Button>
              {remembered.username && (
                <button
                  type="button"
                  className="link-btn"
                  onClick={() => {
                    forgetCredentials();
                    setUsername('');
                    setPassword('');
                  }}
                >
                  清除已保存的凭据
                </button>
              )}
            </form>
          ) : (
            <form onSubmit={submitAdmin} style={{ display: 'flex', flexDirection: 'column', gap: 16 }}>
              <div>
                <h2>服务端管理</h2>
                <p className="auth-sub">
                  管理 Token 在服务端首次启动时生成，可在容器日志或
                  <code className="mono"> admin-token.txt </code>中找到。
                </p>
              </div>
              <Field label="管理 Token" hint="形如 yk_... ，仅在生成时显示一次">
                <input
                  type="password"
                  value={token}
                  onChange={(e) => setToken(e.target.value)}
                  placeholder="yk_..."
                  autoComplete="off"
                  autoFocus
                />
              </Field>
              <label className="checkbox-row">
                <input type="checkbox" checked={remember} onChange={(e) => setRemember(e.target.checked)} />
                记住此设备，下次自动登录
              </label>
              {error && (
                <div className="alert alert-bad" role="alert">
                  {error}
                </div>
              )}
              <Button type="submit" disabled={busy}>
                {busy ? <Loader2 size={16} className="spinner-icon" /> : <ShieldCheck size={16} />}
                {busy ? '正在验证…' : '进入管理控制台'}
              </Button>
              {remembered.hasToken && (
                <button
                  type="button"
                  className="link-btn"
                  onClick={() => {
                    forgetCredentials();
                    setToken('');
                  }}
                >
                  清除已保存的 Token
                </button>
              )}
              <div className="hint-list">
                <span className="row gap-8">
                  <Ticket size={14} /> 没有 Token？在服务端执行 <code className="mono">docker logs yuki-server</code>。
                </span>
              </div>
            </form>
          )}
        </div>
      </div>
    </div>
  );
}
