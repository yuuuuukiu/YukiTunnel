import { Link } from 'react-router-dom';
import { Rocket } from 'lucide-react';

/**
 * Minimal chrome for screens that must be reachable before login (the server
 * deployment wizard). It mirrors the main layout's header so the transition
 * into the app feels continuous.
 */
export default function Shell({ children }: { children: React.ReactNode }) {
  return (
    <div style={{ minHeight: '100%', display: 'flex', flexDirection: 'column' }}>
      <header className="topbar">
        <Link to="/connect" className="row gap-8" style={{ color: 'var(--text)' }}>
          <span className="brand-mark" style={{ width: 26, height: 26, borderRadius: 8, fontSize: 13 }}>Y</span>
          <strong>YukiTunnel</strong>
          <span className="faint small">服务端部署</span>
        </Link>
        <Link to="/connect" className="btn btn-ghost btn-sm">
          <Rocket size={13} /> 已有服务端？去连接
        </Link>
      </header>
      <main className="content" style={{ maxWidth: 1080, width: '100%', margin: '0 auto' }}>
        {children}
      </main>
    </div>
  );
}
