// The Threavia service worker.
//
// It exists for two things: to make the client installable, and to receive the
// Web Push notifications Core sends when something waits for the person. It
// caches nothing. The application is a few hashed files the browser already
// caches, and every piece of state comes from Core: a stale copy of either
// would show an approval that was already answered elsewhere.

self.addEventListener('install', () => {
  // A new worker takes over at once: there is no cache to migrate.
  self.skipWaiting();
});

self.addEventListener('activate', (event) => {
  event.waitUntil(self.clients.claim());
});

self.addEventListener('push', (event) => {
  let message = { title: 'Threavia', body: '', url: '/' };
  try {
    message = { ...message, ...event.data.json() };
  } catch {
    // A payload that is not ours still says something happened.
  }
  event.waitUntil(
    self.registration.showNotification(message.title, {
      body: message.body,
      icon: '/icon-192.png',
      badge: '/icon-64.png',
      // One per Session: a newer notification replaces the older one rather
      // than stacking, and still rings.
      tag: message.tag,
      renotify: Boolean(message.tag),
      data: { url: message.url },
    }),
  );
});

self.addEventListener('notificationclick', (event) => {
  event.notification.close();
  const target = new URL(event.notification.data?.url || '/', self.location.origin).href;
  event.waitUntil(
    (async () => {
      // An open window is brought forward and taken there, rather than a
      // second copy of the client opened beside it.
      const windows = await self.clients.matchAll({ type: 'window', includeUncontrolled: true });
      for (const client of windows) {
        if (new URL(client.url).origin === self.location.origin) {
          await client.focus();
          if ('navigate' in client) await client.navigate(target);
          return;
        }
      }
      await self.clients.openWindow(target);
    })(),
  );
});
