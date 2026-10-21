import { QueryClient, QueryClientProvider } from '@tanstack/react-query';
import { BrowserRouter, Navigate, Outlet, Route, Routes } from 'react-router-dom';
import { AuthProvider, useAuth } from '@web/state/auth';
import { ToastProvider } from '@web/state/toast';
import { Spinner } from '@web/components/ui';
import DesktopLayout from './pages/Layout';
import ConnectPage from './pages/Connect';
import ClientHomePage from './pages/ClientHome';
import DeployPage from './pages/Deploy';
import SettingsPage from './pages/Settings';
import Shell from './pages/Shell';
import AdminOverviewPage from './pages/admin/AdminOverview';
import AdminUsersPage from './pages/admin/Users';
import AdminNodesPage from './pages/admin/Nodes';
import AdminTunnelsPage from './pages/admin/Tunnels';
import AdminSettingsPage from './pages/admin/Settings';
import AuditPage from './pages/admin/Audit';
import ProfilesPage from './pages/admin/Profiles';

const queryClient = new QueryClient({
  defaultOptions: { queries: { retry: 1, refetchOnWindowFocus: true, staleTime: 2000 } },
});

export default function App() {
  return (
    <QueryClientProvider client={queryClient}>
      <ToastProvider>
        <AuthProvider>
          <BrowserRouter>
            <Routes>
              <Route path="/connect" element={<ConnectPage />} />
              {/* Deployment must work before login: it is how a server comes to exist. */}
              <Route path="/deploy" element={<Shell><DeployPage /></Shell>} />
              <Route element={<RequireAuth />}>
                <Route element={<DesktopLayout />}>
                  <Route index element={<ClientHomePage />} />
                  <Route path="settings" element={<SettingsPage />} />
                  {/* Admin console: same pages as the WebUI, so both surfaces
                      stay behaviourally identical. */}
                  <Route element={<RequireAdmin />}>
                    <Route path="admin" element={<AdminOverviewPage />} />
                    <Route path="admin/users" element={<AdminUsersPage />} />
                    <Route path="admin/nodes" element={<AdminNodesPage />} />
                    <Route path="admin/tunnels" element={<AdminTunnelsPage />} />
                    <Route path="admin/settings" element={<AdminSettingsPage />} />
                    <Route path="admin/audit" element={<AuditPage />} />
                    <Route path="admin/profiles" element={<ProfilesPage />} />
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

function RequireAuth() {
  const { user, loading } = useAuth();
  if (loading) {
    return (
      <div style={{ display: 'grid', placeItems: 'center', height: '100vh' }}>
        <Spinner label="正在恢复会话…" />
      </div>
    );
  }
  if (!user) return <Navigate to="/connect" replace />;
  return <Outlet />;
}

function RequireAdmin() {
  const { user, loading } = useAuth();
  if (loading) {
    return (
      <div style={{ display: 'grid', placeItems: 'center', height: '100vh' }}>
        <Spinner />
      </div>
    );
  }
  if (user?.role !== 'admin') return <Navigate to="/" replace />;
  return <Outlet />;
}
