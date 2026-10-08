import { Trash2 } from 'lucide-react';
import { useState } from 'react';

import {
  useConnectedClients,
  usePushConfig,
  useSubscribePush,
  useTestPush,
  useUnsubscribePush,
} from '@/api/queries';
import { ActionError } from '@/components/ui/action-error';
import { Button } from '@/components/ui/button';
import { Input } from '@/components/ui/input';
import { clientId, deviceName, guessedDeviceName, setDeviceName } from '@/lib/device';
import { cn, when } from '@/lib/utils';

const supported =
  typeof window !== 'undefined' &&
  'serviceWorker' in navigator &&
  'PushManager' in window &&
  'Notification' in window;

/** applicationServerKey wants bytes; Core hands out base64url. */
function keyBytes(base64url: string): Uint8Array<ArrayBuffer> {
  const padded = base64url
    .replace(/-/g, '+')
    .replace(/_/g, '/')
    .padEnd(Math.ceil(base64url.length / 4) * 4, '=');
  const raw = atob(padded);
  const bytes = new Uint8Array(new ArrayBuffer(raw.length));
  for (let i = 0; i < raw.length; i++) bytes[i] = raw.charCodeAt(i);
  return bytes;
}

/**
 * This device, the others, and which of them get notified.
 *
 * Core notifies when something waits for the person — an approval, a
 * question — and when work ends, unless they are active on one of their
 * clients: that screen already shows it. Each device is told apart by its own
 * id, so a desktop and a phone, both "web", are two devices; and each opts in
 * to notifications on its own, because only the device can ask its person.
 */
export function NotificationsPane() {
  const config = usePushConfig();
  const connected = useConnectedClients();
  const subscribe = useSubscribePush();
  const unsubscribe = useUnsubscribePush();
  const test = useTestPush();
  const [name, setName] = useState(deviceName);
  const [problem, setProblem] = useState<string | null>(null);
  const [permission, setPermission] = useState(supported ? Notification.permission : 'denied');

  const devices = config.data?.subscriptions ?? [];
  const here = devices.find((device) => device.clientId === clientId);

  const rename = () => {
    const next = name.trim() || guessedDeviceName();
    setName(next);
    setDeviceName(next);
  };

  const enable = async () => {
    setProblem(null);
    try {
      const granted = await Notification.requestPermission();
      setPermission(granted);
      if (granted !== 'granted' || !config.data) return;
      const registration = await navigator.serviceWorker.ready;
      const subscription =
        (await registration.pushManager.getSubscription()) ??
        (await registration.pushManager.subscribe({
          userVisibleOnly: true,
          applicationServerKey: keyBytes(config.data.publicKey),
        }));
      const json = subscription.toJSON();
      await subscribe.mutateAsync({
        endpoint: subscription.endpoint,
        keys: { p256dh: json.keys?.p256dh ?? '', auth: json.keys?.auth ?? '' },
        label: deviceName(),
      });
    } catch (error) {
      setProblem(error instanceof Error ? error.message : String(error));
    }
  };

  const disable = async (id: string) => {
    setProblem(null);
    try {
      const registration = await navigator.serviceWorker.ready;
      await (await registration.pushManager.getSubscription())?.unsubscribe();
    } catch {
      // Core forgets it either way; the browser's own record is a courtesy.
    }
    unsubscribe.mutate(id);
  };

  return (
    <div className="space-y-4">
      <div className="space-y-1.5">
        <label htmlFor="device-name" className="text-muted text-xs">
          This device is called
        </label>
        <div className="flex gap-2">
          <Input
            id="device-name"
            value={name}
            onChange={(event) => setName(event.target.value)}
            onBlur={rename}
            onKeyDown={(event) => event.key === 'Enter' && rename()}
            placeholder={guessedDeviceName()}
          />
        </div>
        <p className="text-muted text-xs">
          A browser cannot read its machine&apos;s name: put the hostname here if that is how you
          know it. It applies from the next request.
        </p>
      </div>

      {(connected.data?.length ?? 0) > 0 && (
        <div className="space-y-1">
          <p className="text-muted text-xs">Connected now</p>
          <ul className="space-y-0.5 text-sm">
            {connected.data?.map((client, index) => (
              <li key={client.id || index} className="flex items-center gap-2">
                <span
                  className={cn('size-1.5 shrink-0 rounded-full', client.active ? 'bg-ok' : 'bg-border')}
                  title={client.active ? 'In use' : 'Open but idle'}
                />
                <span className="min-w-0 flex-1 truncate">
                  {client.name || `Unnamed ${client.channel} client`}
                  {client.id === clientId && <span className="text-muted"> (this one)</span>}
                </span>
                <span className="text-muted text-xs">{client.active ? 'in use' : 'idle'}</span>
              </li>
            ))}
          </ul>
        </div>
      )}

      {!supported ? (
        <p className="text-muted text-sm">
          This browser cannot receive notifications. On an iPhone, add Threavia to the home screen
          first and open it from there.
        </p>
      ) : (
        <div className="space-y-2">
          <div className="flex flex-wrap items-center gap-2">
            {here ? (
              <>
                <span className="text-sm">This device gets notifications.</span>
                <Button variant="ghost" size="sm" onClick={() => void disable(here.id)}>
                  Turn off here
                </Button>
              </>
            ) : (
              <Button
                variant="secondary"
                size="sm"
                disabled={subscribe.isPending || !config.data || permission === 'denied'}
                onClick={() => void enable()}
              >
                Notify this device
              </Button>
            )}
            {devices.length > 0 && (
              <Button variant="ghost" size="sm" disabled={test.isPending} onClick={() => test.mutate()}>
                Send a test
              </Button>
            )}
          </div>
          {permission === 'denied' && (
            <p className="text-warn-text text-xs">
              Notifications are blocked for this site; allow them in the browser settings.
            </p>
          )}
          {problem && (
            <p role="alert" className="text-danger text-sm">
              {problem}
            </p>
          )}
          <ActionError error={test.error ?? unsubscribe.error} />

          {devices.length > 0 && (
            <ul className="space-y-1">
              {devices.map((device) => (
                <li key={device.id} className="flex items-center gap-2 text-sm">
                  <span className="min-w-0 flex-1 truncate">
                    {device.label || 'Unnamed device'}
                    {device.clientId === clientId && <span className="text-muted"> (this one)</span>}
                  </span>
                  <span className={device.lastError ? 'text-warn-text text-xs' : 'text-muted text-xs'}>
                    {device.lastError
                      ? `last delivery failed: ${device.lastError}`
                      : device.lastUsedAt
                        ? `last notified ${when(device.lastUsedAt)}`
                        : 'not notified yet'}
                  </span>
                  <Button
                    variant="ghost"
                    size="icon"
                    title="Stop notifying this device"
                    onClick={() =>
                      device.clientId === clientId ? void disable(device.id) : unsubscribe.mutate(device.id)
                    }
                  >
                    <Trash2 />
                  </Button>
                </li>
              ))}
            </ul>
          )}
        </div>
      )}
    </div>
  );
}
