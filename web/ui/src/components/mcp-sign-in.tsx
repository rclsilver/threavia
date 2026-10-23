import { CheckCircle2, LoaderCircle, XCircle } from 'lucide-react';
import type { MCPSignIn } from '@/auth/mcp';
import { ActionError } from '@/components/ui/action-error';
import { Button } from '@/components/ui/button';
import { Dialog, DialogContent, DialogDescription, DialogTitle } from '@/components/ui/dialog';

export function MCPSignInDialog({ signIn, serverName, backendName, open, onOpenChange, onConnect, onReview, starting, actionError }: {
  signIn: MCPSignIn;
  serverName: string;
  backendName: string;
  open: boolean;
  onOpenChange: (open: boolean) => void;
  onConnect?: () => void;
  onReview: () => void;
  starting: boolean;
  actionError: Error | null;
}) {
  if (!signIn.flow) return null;
  const problem = signIn.flow.problem;
  const busy = signIn.finishing || signIn.checking;
  const failed = !!problem || !!signIn.finishError;
  const title = problem === 'denied' ? 'Connection cancelled' : failed ? `Could not connect ${serverName}` : signIn.connected ? `${serverName} account connected` : `Connecting ${serverName}`;
  return <Dialog open={open} onOpenChange={onOpenChange}>
    <DialogContent>
      <DialogTitle className="pr-6">{title}</DialogTitle>
      <DialogDescription>This connection is for {serverName} on {backendName}.</DialogDescription>
      <div role="status" aria-live="polite" className="flex items-start gap-3 text-sm">
        {busy ? <LoaderCircle aria-hidden="true" className="text-accent size-5 shrink-0 animate-spin" /> : failed ? <XCircle aria-hidden="true" className="text-muted size-5 shrink-0" /> : <CheckCircle2 aria-hidden="true" className="text-accent size-5 shrink-0" />}
        <div className="space-y-2">
          {signIn.finishing && <p>Finishing sign-in automatically…</p>}
          {signIn.checking && <p>Checking available tools…</p>}
          {signIn.discovery && <><p>{signIn.discovery.tools.length} tools available.</p><p className="text-muted">Start a new message to use this connection. Tool permissions stay as configured.</p></>}
          {problem === 'denied' && <p>Access was not granted. You can connect again when ready.</p>}
          {problem === 'invalid' && <p>This sign-in return is missing or no longer matches the connection you started. Start a new connection.</p>}
          {problem === 'provider' && <p>The provider did not complete sign-in. Start a new connection to try again.</p>}
          <ActionError error={signIn.finishError} outcome="Sign-in could not be confirmed" recovery="Start a new connection to try again." />
          <ActionError error={signIn.discoveryError} outcome="Your account is connected, but its tools could not be loaded" recovery="Check the connection again." />
          <ActionError error={actionError} outcome="Connection action could not be completed" recovery="Try connecting again once the backend is ready." />
        </div>
      </div>
      <div className="flex flex-wrap justify-end gap-2 border-t border-border pt-4">
        <Button size="lg" onClick={() => onOpenChange(false)}>{busy ? 'Close' : 'Done'}</Button>
        {failed && onConnect && <Button variant="primary" size="lg" disabled={starting} onClick={onConnect}>{starting ? 'Opening sign-in…' : 'Connect again…'}</Button>}
        {signIn.discoveryError && <Button variant="primary" size="lg" disabled={busy} onClick={signIn.recheck}>Check connection again</Button>}
        {signIn.discovery && <Button variant="primary" size="lg" onClick={onReview}>Review tools</Button>}
      </div>
    </DialogContent>
  </Dialog>;
}
