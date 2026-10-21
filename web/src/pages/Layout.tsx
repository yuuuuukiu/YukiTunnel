import { NavLink, Outlet, useLocation, useNavigate } from 'react-router-dom';
import {
  Activity, Boxes, ChevronRight, Cloud, FileClock, Globe2, LayoutDashboard, LogOut,
  Network, ServerCog, ShieldCheck, UserRound, Users,
} from 'lucide-react';
import { useAuth } from '../state/auth';
import { useToast } from '../state/toast';
import { Badge } from '../components/ui';

interface NavEntry {
  to: string;
  label: string;
  icon: React.ReactNode;
  end?: boolean;
}

const userNav: NavEntry[] = [
  { to: '/', label: '概览', icon: <LayoutDashboard size={17} />, end: true },
  { to: '/servers', label: '节点', icon: <Network size={17} /> },
  { to: '/tunnels', label: '我的隧道', icon: <Activity size={17} /> },
  { to: '/account', label: '账号与密钥', icon: <UserRound size={17} /> },
];

const adminNav: NavEntry[] = [
  { to: '/admin', label: '管理概览', icon: <ServerCog size={17} />, end: true },
  { to: '/admin/users', label: '用户管理', icon: <Users size={17} /> },
  { to: '/admin/nodes', label: '节点管理', icon: <Boxes size={17} /> },
  { to: '/admin/tunnels', label: '隧道管理', icon: <Globe2 size={17} /> },
  { to: '/admin/settings', label: '域名与端口', icon: <ShieldCheck size={17} /> },
  { to: '/admin/profiles', label: '云 Profile', icon: <Cloud size={17} /> },
  { to: '/admin/audit', label: '审计日志', icon: <FileClock size={17} /> },
];

export default function AppLayout() {
  const { user, logout } = useAuth();
  const toast = useToast();
  const navigate = useNavigate();
  const location = useLocation();

  const doLogout = async () => {
    await logout();
    toast.info('已退出登录');
    navigate('/login', { replace: true });
  };

  return (
    <div className="app-shell">
      <aside className="sidebar">
        <div className="brand">
          <div className="brand-mark">Y</div>
          <div className="brand-text">
            <strong>YukiTunnel</strong>
            <span>管理平台</span>
          </div>
        </div>
        <nav className="nav">
          {userNav.map((item) => (
            <NavLink key={item.to} to={item.to} end={item.end} className={({ isActive }) => `nav-item ${isActive ? 'active' : ''}`}>
              {item.icon}
              {item.label}
            </NavLink>
          ))}
        </nav>
        {user?.role === 'admin' && (
          <>
            <div className="nav-section">管理</div>
            <nav className="nav">
              {adminNav.map((item) => (
                <NavLink key={item.to} to={item.to} end={item.end} className={({ isActive }) => `nav-item ${isActive ? 'active' : ''}`}>
                  {item.icon}
                  {item.label}
                </NavLink>
              ))}
            </nav>
          </>
        )}
        <div className="sidebar-footer">
          <div className="row gap-8" style={{ padding: '0 8px 10px' }}>
            <div className="dot dot-good" />
            <div style={{ minWidth: 0 }}>
              <div style={{ fontSize: 13, fontWeight: 600, overflow: 'hidden', textOverflow: 'ellipsis' }}>
                {user?.display_name || user?.username}
              </div>
              <div className="faint small">{user?.role === 'admin' ? '管理员' : '普通用户'}</div>
            </div>
          </div>
          <button className="nav-item" onClick={doLogout}>
            <LogOut size={17} />
            退出登录
          </button>
        </div>
      </aside>
      <div className="main">
        <header className="topbar">
          <div className="row gap-8 small muted">
            <span>YukiTunnel</span>
            <ChevronRight size={13} />
            <span>{titleFor(location.pathname)}</span>
          </div>
          <div className="row gap-12">
            <Badge tone="good">
              <span className="dot dot-good" /> 服务端已连接
            </Badge>
            {user?.role === 'admin' && <Badge tone="info">管理员</Badge>}
          </div>
        </header>
        <main className="content">
          <Outlet />
        </main>
      </div>
    </div>
  );
}

function titleFor(path: string): string {
  if (path.startsWith('/admin/users')) return '用户管理';
  if (path.startsWith('/admin/nodes')) return '节点管理';
  if (path.startsWith('/admin/tunnels')) return '隧道管理';
  if (path.startsWith('/admin/settings')) return '域名与端口';
  if (path.startsWith('/admin/profiles')) return '云 Profile';
  if (path.startsWith('/admin/audit')) return '审计日志';
  if (path.startsWith('/admin')) return '管理概览';
  if (path.startsWith('/servers')) return '节点';
  if (path.startsWith('/tunnels')) return '我的隧道';
  if (path.startsWith('/account')) return '账号与密钥';
  return '概览';
}
