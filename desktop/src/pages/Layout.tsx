import { NavLink, Outlet } from 'react-router-dom';
import {
  Activity, Boxes, Cloud, FileClock, Globe2, LogOut, Rocket, ServerCog,
  Settings, ShieldCheck, Users,
} from 'lucide-react';
import { useAuth } from '@web/state/auth';
import { Badge } from '@web/components/ui';

/** Primary navigation, always visible. */
const clientNav = [
  { to: '/', label: '我的隧道', icon: Activity, end: true },
  { to: '/settings', label: '设置', icon: Settings, end: false },
  { to: '/deploy', label: '部署服务端', icon: Rocket, end: false },
];

/** Admin console entries, shown only to administrators. Mirrors the WebUI. */
const adminNav = [
  { to: '/admin', label: '管理概览', icon: ServerCog, end: true },
  { to: '/admin/users', label: '用户管理', icon: Users, end: false },
  { to: '/admin/nodes', label: '节点管理', icon: Boxes, end: false },
  { to: '/admin/tunnels', label: '隧道管理', icon: Globe2, end: false },
  { to: '/admin/settings', label: '域名与端口', icon: ShieldCheck, end: false },
  { to: '/admin/profiles', label: '云 Profile', icon: Cloud, end: false },
  { to: '/admin/audit', label: '审计日志', icon: FileClock, end: false },
];

export default function DesktopLayout() {
  const { user, serverUrl, logout } = useAuth();
  const isAdmin = user?.role === 'admin';

  return (
    <div className="app-shell">
      <aside className="sidebar">
        <div className="brand">
          <div className="brand-mark">Y</div>
          <div className="brand-text">
            <strong>YukiTunnel</strong>
            <span>{isAdmin ? '桌面管理端' : '桌面客户端'}</span>
          </div>
        </div>

        <nav className="nav">
          {clientNav.map(({ to, label, icon: Icon, end }) => (
            <NavLink key={to} to={to} end={end} className={({ isActive }) => `nav-item ${isActive ? 'active' : ''}`}>
              <Icon size={17} /> {label}
            </NavLink>
          ))}
        </nav>

        {isAdmin && (
          <>
            <div className="nav-section">管理</div>
            <nav className="nav">
              {adminNav.map(({ to, label, icon: Icon, end }) => (
                <NavLink key={to} to={to} end={end} className={({ isActive }) => `nav-item ${isActive ? 'active' : ''}`}>
                  <Icon size={17} /> {label}
                </NavLink>
              ))}
            </nav>
          </>
        )}

        <div className="sidebar-footer">
          <div className="row gap-8 space-between" style={{ padding: '0 8px 10px', alignItems: 'center' }}>
            <div className="row gap-8" style={{ minWidth: 0, flex: 1, alignItems: 'center' }}>
              <ShieldCheck size={16} className="faint" style={{ flexShrink: 0 }} />
              <div style={{ minWidth: 0 }}>
                <div style={{ fontSize: 13, fontWeight: 600, display: 'flex', alignItems: 'center', gap: 6 }}>
                  {user?.username}
                  {isAdmin && <Badge tone="info">管理员</Badge>}
                </div>
                <div className="faint small" style={{ overflow: 'hidden', textOverflow: 'ellipsis', whiteSpace: 'nowrap' }} title={serverUrl ?? ''}>
                  {serverUrl}
                </div>
              </div>
            </div>
            <button
              type="button"
              className="icon-btn"
              title="断开并切换服务端"
              onClick={() => void logout()}
              style={{ flexShrink: 0, padding: 4 }}
            >
              <LogOut size={15} />
            </button>
          </div>
        </div>
      </aside>

      <div className="main">
        <header className="topbar">
          <div className="small muted">
            YukiTunnel · {isAdmin ? '桌面管理端' : '桌面客户端'}
          </div>
          <div className="small faint">关闭窗口将最小化到托盘，穿透继续运行</div>
        </header>
        <main className="content">
          <Outlet />
        </main>
      </div>
    </div>
  );
}
