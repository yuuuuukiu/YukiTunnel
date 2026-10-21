import { QueryClient, QueryClientProvider } from '@tanstack/react-query';
import { BrowserRouter, Navigate, Outlet, Route, Routes, useLocation } from 'react-router-dom';
import { AuthProvider, useAuth } from './state/auth';
import { ToastProvider } from './state/toast';
import AppLayout from './pages/Layout';
import LoginPage from './pages/Login';
import DashboardPage from './pages/Dashboard';
import ServersPage from './pages/Servers';
import TunnelsPage from './pages/Tunnels';
import TunnelDetailPage from './pages/TunnelDetail';
import AccountPage from './pages/Account';
import AdminOverviewPage from './pages/admin/AdminOverview';
import AdminUsersPage from './pages/admin/Users';
import AdminNodesPage from './pages/admin/Nodes';
import AdminTunnelsPage from './pages/admin/Tunnels';
import AdminSettingsPage from './pages/admin/Settings';
import AuditPage from './pages/admin/Audit';
import ProfilesPage from './pages/admin/Profiles';
import { Spinner } from './components/ui';

const queryClient = new QueryClient({
  defaultOptions: {
    queries: { retry: 1, refetchOnWindowFocus: true, staleTime: 2000 },
  },
});

export default function App() {
  return (
    <QueryClientProvider client={queryClient}>
      <ToastProvider>
        <AuthProvider>
          <BrowserRouter>
            <Routes>
              <Route path="/login" element={<GuestOnly />} />
              <Route element={<RequireAuth />}>
                <Route element={<AppLayout />}>
                  <Route index element={<DashboardPage />} />
                  <Route path="servers" element={<ServersPage />} />
                  <Route path="tunnels" element={<TunnelsPage />} />
                  <Route path="tunnels/:id" element={<TunnelDetailPage />} />
                  <Route path="account" element={<AccountPage />} />
                  <Route element={<RequireAdmin />}>
                    <Route path="admin" element={<AdminOverviewPage />} />
                    <Route path="admin/users" element={<AdminUsersPage />} />
                    <Route path="admin/nodes" element={<AdminNodesPage />} />
                    <Route path="admin/tunnels" element={<AdminTunnelsPage />} />
                    <Route path="admin/settings" element={<AdminSettingsPage />} />
                    <Route path="admin/profiles" element={<ProfilesPage />} />
                    <Route path="admin/audit" element={<AuditPage />} />
                  </Route>
                </Route>
              </Route>
              <Route path="*" element={<Navigate to="/" replace />} />
            </Routes>
          </BrowserRouter>
        </AuthProvider>
      </ToastProvider>
    </QueryClientProvider>
  );
}

/** Keeps an already-authenticated user away from the login form. */
function GuestOnly() {
  const { user, loading } = useAuth();
  if (loading) {
    return (
      <div style={{ display: 'grid', placeItems: 'center', height: '100vh' }}>
        <Spinner label="正在恢复会话…" />
      </div>
    );
  }
  if (user) return <Navigate to="/" replace />;
  return <LoginPage />;
}

function RequireAuth() {
  const { user, loading } = useAuth();
  const location = useLocation();
  if (loading) {
    return (
      <div style={{ display: 'grid', placeItems: 'center', height: '100vh' }}>
        <Spinner label="正在恢复会话…" />
      </div>
    );
  }
  if (!user) return <Navigate to="/login" replace state={{ from: location.pathname }} />;
  return <Outlet />;
}

function RequireAdmin() {
  const { user } = useAuth();
  if (user?.role !== 'admin') return <Navigate to="/" replace />;
  return <Outlet />;
}
