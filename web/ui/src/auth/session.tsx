import { createContext, useCallback, useEffect, useRef, useState } from 'react';
import type { ReactNode } from 'react';

import { setBearer } from '@/api/client';
import { ActionError } from '@/components/ui/action-error';
import { Button } from '@/components/ui/button';
import { setLandingPath } from '@/auth/landing';
import { CALLBACK_PATH, complete, login, refresh, type AuthPublic, type Tokens } from '@/auth/oidc';

/**
 * Where the client stands with the provider.
 *
 * `open` is a deployment that asks for nothing — Core attributes every request
 * to one configured user — and the whole flow below is skipped. It is the
 * default shape of a laptop install, so it must cost nothing.
 */
type Status = 'checking' | 'open' | 'signing-in' | 'ready' | 'failed';

interface Session {
  status: Status;
  error?: string;
  signIn: () => void;
}

// Exposed through the provider only: nothing outside it reads the session yet,
// and a context no one consumes is a context that drifts.
const SessionContext = createContext<Session>({ status: 'checking', signIn: () => {} });

/**
 * Obtains a token when the deployment wants one, and keeps it fresh.
 *
 * It sits above everything that talks to Core, because a request sent before
 * the token exists is a request that comes back 401 and teaches the person
 * nothing. Nothing renders until this knows which world it is in.
 */
export function SessionProvider({ children }: { children: ReactNode }) {
  const [status, setStatus] = useState<Status>('checking');
  const [error, setError] = useState<string>();
  const config = useRef<AuthPublic>({ mode: 'none' });
  const renewal = useRef<ReturnType<typeof setTimeout>>(undefined);

  // Holds the tokens and arms the renewal. Memory only: a reload starts a fresh
  // redirect, which the provider answers silently while its session stands, and
  // which leaves nothing on disk for a script on this page to find.
  const hold = useCallback((tokens: Tokens) => {
    setBearer(tokens.accessToken);
    clearTimeout(renewal.current);

    if (!tokens.refreshToken) return;
    // A minute of margin, and never a timer so short it spins.
    const delay = Math.max(tokens.expiresAt - Date.now() - 60_000, 15_000);
    renewal.current = setTimeout(() => {
      refresh(config.current, tokens.refreshToken!)
        .then(hold)
        // A refresh that fails means the provider session is over. Starting the
        // flow again is the honest answer, and it is silent when it can be.
        .catch(() => void login(config.current));
    }, delay);
  }, []);

  const signIn = useCallback(() => {
    setStatus('signing-in');
    login(config.current).catch((cause: unknown) => {
      setError(cause instanceof Error ? cause.message : String(cause));
      setStatus('failed');
    });
  }, []);

  useEffect(() => {
    let cancelled = false;

    const start = async () => {
      try {
        // Unauthenticated by necessity: this is how the client learns there is
        // anything to authenticate against.
        const response = await fetch('/api/v1/auth');
        if (!response.ok) throw new Error(`core did not answer (${response.status})`);
        const found = (await response.json()) as AuthPublic;
        if (cancelled) return;
        config.current = found;

        if (found.mode !== 'oidc') {
          setStatus('open');
          return;
        }

        if (window.location.pathname === CALLBACK_PATH) {
          const { tokens, to } = await complete(found);
          if (cancelled) return;
          hold(tokens);
          // Where to land is left for the callback route to act on: the router
          // captured the location when it was created, so rewriting history
          // here would leave it showing a page nobody asked for.
          setLandingPath(to);
          setStatus('ready');
          return;
        }

        await login(found);
      } catch (cause: unknown) {
        if (cancelled) return;
        setError(cause instanceof Error ? cause.message : String(cause));
        setStatus('failed');
      }
    };

    void start();
    return () => {
      cancelled = true;
      clearTimeout(renewal.current);
    };
  }, [hold]);

  if (status === 'checking' || status === 'signing-in') {
    return <Waiting>Signing in…</Waiting>;
  }

  if (status === 'failed') {
    return (
      <Waiting>
        <ActionError error={error ? new Error(error) : null} outcome="Not signed in" recovery="Nothing was changed; sign in again." />
        <Button size="lg" onClick={signIn}>
          Try again
        </Button>
      </Waiting>
    );
  }

  return (
    <SessionContext.Provider value={{ status, error, signIn }}>{children}</SessionContext.Provider>
  );
}

function Waiting({ children }: { children: ReactNode }) {
  return (
    <div className="text-muted flex h-full flex-col items-center justify-center gap-3 p-6 text-sm">
      {children}
    </div>
  );
}
