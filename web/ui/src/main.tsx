import { QueryClient, QueryClientProvider } from '@tanstack/react-query';
import { RouterProvider } from '@tanstack/react-router';
import { StrictMode } from 'react';
import { createRoot } from 'react-dom/client';

import { setTransport } from './api/client';
import { SessionProvider } from './auth/session';
import { DEMO } from './demo/mode';
import { router } from './router';
import { StreamProvider } from './stream-provider';
import './styles.css';
// Applies the stored layout before anything is laid out.
import './use-layout';

const queries = new QueryClient({
  defaultOptions: {
    queries: {
      // The event stream is what keeps the cache current, so a view does not
      // poll and a window regaining focus does not refetch what is already
      // correct.
      refetchOnWindowFocus: false,
      staleTime: 30_000,
      retry: 1,
    },
  },
});

const root = document.getElementById('root');
if (!root) throw new Error('the page has no mount point');

/**
 * The demo is the same client with Core played in the browser: no sign-in, no
 * stream, no worker, and every request answered from fictitious data. Loaded
 * only on its own path, so the real client carries none of it.
 */
async function demo(mount: HTMLElement) {
  const server = await import('./demo/server');
  setTransport({
    request: server.demoRequest,
    upload: server.demoUpload,
    href: server.demoHref,
    changed: () => void queries.invalidateQueries(),
  });
  createRoot(mount).render(
    <StrictMode>
      <QueryClientProvider client={queries}>
        <StreamProvider demo>
          <RouterProvider router={router} />
        </StreamProvider>
      </QueryClientProvider>
    </StrictMode>,
  );
}

if (DEMO) {
  void demo(root);
} else {
  createRoot(root).render(
    <StrictMode>
      <QueryClientProvider client={queries}>
        {/* Above the stream and the router: both talk to Core, and a request
            sent before the token exists comes back 401 having taught nobody
            anything. */}
        <SessionProvider>
          <StreamProvider>
            <RouterProvider router={router} />
          </StreamProvider>
        </SessionProvider>
      </QueryClientProvider>
    </StrictMode>,
  );
}

// The worker makes the client installable and receives the notifications Core
// pushes. It caches nothing, so registering it changes nothing else.
if (!DEMO && 'serviceWorker' in navigator) {
  window.addEventListener('load', () => {
    navigator.serviceWorker.register('/sw.js').catch(() => {
      // Without it the client still works; it only cannot be installed or
      // notified, which the Notifications section says.
    });
  });
}
