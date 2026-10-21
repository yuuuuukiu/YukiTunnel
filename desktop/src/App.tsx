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
