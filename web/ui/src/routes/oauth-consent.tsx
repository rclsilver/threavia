import { useMutation, useQuery } from '@tanstack/react-query';
import { KeyRound } from 'lucide-react';
import { useState } from 'react';

import { api } from '@/api/client';
import type { components } from '@/api/schema';
import { CONTROL_SCOPES, type ControlScope } from '@/lib/control-scopes';
import { ActionError } from '@/components/ui/action-error';
import { Button } from '@/components/ui/button';
import { CheckboxField } from '@/components/ui/checkbox';

type OAuthClient = components['schemas']['OAuthClient'];
type Scope = ControlScope;

/** The query Core's /oauth/authorize hands over, as the client sent it. */
export interface ConsentSearch {
  client_id?: string;
  redirect_uri?: string;
  state?: string;
  code_challenge?: string;
  code_challenge_method?: string;
  scope?: string;
  resource?: string;
}

/**
 * Where an MCP client such as ChatGPT asks to act on this account.
 *
 * The person is already signed in to the Web client, as for any page. They
 * choose the rights: read only unless they tick more, whatever the client
 * asked for. The grant then appears in Settings → MCP access, to be revoked
 * there.
 */
export function OAuthConsentView() {
  // Read raw: the router would parse values as JSON, and a client's state must
  // go back exactly as it came.
  const [search] = useState(() => consentSearch(window.location.search));
  const client = useQuery({
    queryKey: ['oauth-client', search.client_id],
    queryFn: () => api.get<OAuthClient>(`/api/v1/oauth/clients/${encodeURIComponent(search.client_id ?? '')}`),
    enabled: Boolean(search.client_id),
    retry: false,
  });
  const requested = (search.scope ?? '').split(' ').filter((scope): scope is Scope => CONTROL_SCOPES.some((known) => known.value === scope));
  const [granted, setGranted] = useState<Scope[]>(['read']);
  const decide = useMutation({
    mutationFn: (approved: boolean) =>
      api.post<{ redirectTo: string }>('/api/v1/oauth/authorize', {
        clientId: search.client_id,
        redirectUri: search.redirect_uri,
        state: search.state,
        codeChallenge: search.code_challenge,
        codeChallengeMethod: search.code_challenge_method,
        resource: search.resource,
        scopes: granted,
        approved,
      }),
    onSuccess: (answer) => window.location.assign(answer.redirectTo),
  });

  const destination = hostOf(search.redirect_uri);
  const registered = client.data?.redirectUris.includes(search.redirect_uri ?? '');
  const invalid = !search.client_id || !search.redirect_uri || client.isError || (client.data && !registered);

  return (
    <div className="flex min-h-0 flex-1 flex-col items-center overflow-y-auto p-4 sm:p-8">
      <section className="border-border bg-surface w-full max-w-lg space-y-5 rounded-xl border p-5 sm:p-6" aria-labelledby="consent-title">
        <header className="flex items-start gap-3">
          <KeyRound className="text-muted mt-1 size-5 shrink-0" />
          <div className="min-w-0 space-y-1">
            <h1 id="consent-title" className="text-lg font-semibold">
              {client.data ? `Connect ${client.data.name} to Threavia?` : 'Connect an MCP client to Threavia?'}
            </h1>
            {destination && (
              <p className="text-muted text-sm">
                It will act on your account through <code>/mcp</code>. You return to <strong className="text-text break-all">{destination}</strong> afterwards.
              </p>
            )}
          </div>
        </header>

        {client.isPending && search.client_id && <p role="status" className="text-muted text-sm">Checking the client…</p>}
        {invalid && !client.isPending && (
          <p role="alert" className="text-danger text-sm">
            This connection request is not valid: the client is unknown or its return address was not registered. Start the connection again from the client.
          </p>
        )}

        {client.data && registered && (
          <>
            <p className="text-muted text-sm">
              The name was given by the client itself. Continue only if you started this connection from <strong className="text-text">{destination}</strong>.
            </p>
            <fieldset className="space-y-2 [&_label]:min-h-11">
              <legend className="mb-2 text-sm font-medium">Rights on this Core</legend>
              {CONTROL_SCOPES.map((scope) => (
                <CheckboxField
                  key={scope.value}
                  checked={granted.includes(scope.value)}
                  onCheckedChange={(checked) => setGranted(checked ? [...granted, scope.value] : granted.filter((value) => value !== scope.value))}
                >
                  {scope.label}
                  {requested.includes(scope.value) && <span className="text-muted"> · requested</span>}
                </CheckboxField>
              ))}
            </fieldset>
            <p className="text-muted text-sm">
              Rights cover your resources across all your projects. You can revoke this connection at any time in Settings → MCP access.
            </p>
            <ActionError error={decide.error} outcome="Not connected" recovery="Try again, or start the connection again from the client." />
            <div className="flex flex-wrap gap-2">
              <Button variant="primary" size="lg" disabled={decide.isPending || !granted.length} onClick={() => decide.mutate(true)}>
                Allow
              </Button>
              <Button size="lg" disabled={decide.isPending} onClick={() => decide.mutate(false)}>
                Deny
              </Button>
            </div>
          </>
        )}
      </section>
    </div>
  );
}

function hostOf(uri: string | undefined): string {
  try {
    return uri ? new URL(uri).host : '';
  } catch {
    return '';
  }
}

function consentSearch(query: string): ConsentSearch {
  const params = new URLSearchParams(query);
  const value = (name: string) => params.get(name) ?? undefined;
  return {
    client_id: value('client_id'),
    redirect_uri: value('redirect_uri'),
    state: value('state'),
    code_challenge: value('code_challenge'),
    code_challenge_method: value('code_challenge_method'),
    scope: value('scope'),
    resource: value('resource'),
  };
}
