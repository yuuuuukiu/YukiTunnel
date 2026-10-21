import { NavLink, Outlet } from 'react-router-dom';
import { Activity, Rocket, Settings, ShieldCheck } from 'lucide-react';
import { useAuth } from '@web/state/auth';

export default function DesktopLayout() {
  const { user, serverUrl } = useAuth();
  return (
    <div className="app-shell">
      <aside className="sidebar">
        <div className="brand">
          <div className="brand-mark">Y</div>
          <div className="brand-text">
            <strong>YukiTunnel</strong>
            <span>桌面客户端</span>
          </div>
        </div>
        <nav className="nav">
          <NavLink to="/" end className={({ isActive }) => `nav-item ${isActive ? 'active' : ''}`}>
            <Activity size={17} /> 我的隧道
          </NavLink>
          <NavLink to="/deploy" className={({ isActive }) => `nav-item ${isActive ? 'active' : ''}`}>
            <Rocket size={17} /> 部署服务端
          </NavLink>
          <NavLink to="/settings" className={({ isActive }) => `nav-item ${isActive ? 'active' : ''}`}>
            <Settings size={17} /> 设置
          </NavLink>
        </nav>
        <div className="sidebar-footer">
          <div className="row gap-8" style={{ padding: '0 8px 10px' }}>
            <ShieldCheck size={16} className="faint" />
            <div style={{ minWidth: 0 }}>
              <div style={{ fontSize: 13, fontWeight: 600 }}>{user?.username}</div>
              <div className="faint small" style={{ overflow: 'hidden', textOverflow: 'ellipsis' }}>
                {serverUrl}
              </div>
            </div>
          </div>
        </div>
      </aside>
      <div className="main">
        <header className="topbar">
          <div className="small muted">YukiTunnel · 桌面客户端</div>
          <div className="small faint">关闭窗口将最小化到托盘，穿透继续运行</div>
        </header>
        <main className="content">
          <Outlet />
        </main>
      </div>
    </div>
  );
}
