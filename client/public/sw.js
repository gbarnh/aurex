// Aurex service worker — handles push notifications and deep-links taps back
// into the right session.

self.addEventListener('install', (event) => {
  self.skipWaiting();
});

self.addEventListener('activate', (event) => {
  event.waitUntil(self.clients.claim());
});

self.addEventListener('push', (event) => {
  let payload = {};
  try {
    payload = event.data ? event.data.json() : {};
  } catch (err) {
    payload = { title: 'Aurex', body: event.data ? event.data.text() : '' };
  }
  const title = payload.title || 'Aurex';
  const options = {
    body: payload.body || '',
    tag: payload.tag || 'aurex',
    renotify: true,
    data: { sessionId: payload.sessionId || '' },
    icon: '/icon-192.png',
    badge: '/badge-72.png',
  };
  event.waitUntil(self.registration.showNotification(title, options));
});

self.addEventListener('notificationclick', (event) => {
  event.notification.close();
  const sessionId = event.notification?.data?.sessionId || '';
  const url = sessionId ? `/?session=${sessionId}` : '/';

  event.waitUntil(
    self.clients.matchAll({ type: 'window', includeUncontrolled: true }).then((clients) => {
      for (const client of clients) {
        const clientUrl = new URL(client.url);
        if (clientUrl.origin === self.location.origin && 'focus' in client) {
          client.navigate(url).catch(() => {});
          return client.focus();
        }
      }
      if (self.clients.openWindow) {
        return self.clients.openWindow(url);
      }
      return undefined;
    })
  );
});
