import { StrictMode } from 'react';
import { createRoot } from 'react-dom/client';
import '@web/styles.css';
import App from './App';
import { installDesktopTransport, readInsecure } from './lib/transport';

installDesktopTransport(readInsecure);

createRoot(document.getElementById('root')!).render(
  <StrictMode>
    <App />
  </StrictMode>,
);
