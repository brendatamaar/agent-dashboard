const CACHE_NAME = 'agent-dashboard-v1';
const STATIC_ASSETS = ['/', '/index.html', '/manifest.webmanifest', '/icon.svg'];

self.addEventListener('install', (event) => {
  event.waitUntil(
    caches.open(CACHE_NAME).then((cache) => {
      return cache.addAll(STATIC_ASSETS);
    })
  );
  self.skipWaiting();
});

self.addEventListener('activate', (event) => {
  event.waitUntil(
    caches.keys().then((keys) => {
      return Promise.all(
        keys.map((key) => {
          if (key !== CACHE_NAME) {
            return caches.delete(key);
          }
        })
      );
    })
  );
  self.clients.claim();
});

self.addEventListener('fetch', (event) => {
  // Only cache static navigation or assets, never intercept /api/ or /healthz
  const url = new URL(event.request.url);
  if (url.pathname.startsWith('/api/') || url.pathname.startsWith('/healthz') || url.pathname.startsWith('/ws/')) {
    return;
  }

  event.respondWith(
    fetch(event.request).catch(() => {
      return caches.match(event.request).then((res) => {
        return res || caches.match('/index.html');
      });
    })
  );
});

// Push Notification handling
self.addEventListener('push', (event) => {
  if (!event.data) return;

  let payload = {
    title: 'agent-dashboard',
    body: 'Agent activity update',
    icon: '/icon.svg',
    target_url: '/',
  };

  try {
    payload = event.data.json();
  } catch {
    payload.body = event.data.text();
  }

  const options = {
    body: payload.body,
    icon: payload.icon || '/icon.svg',
    badge: '/icon.svg',
    tag: payload.tag || 'agent-notification',
    data: {
      url: payload.target_url || '/',
    },
  };

  event.waitUntil(self.registration.showNotification(payload.title, options));
});

// Notification click deep-links to agent card or home
self.addEventListener('notificationclick', (event) => {
  event.notification.close();
  const targetURL = (event.notification.data && event.notification.data.url) || '/';

  event.waitUntil(
    self.clients.matchAll({ type: 'window', includeUncontrolled: true }).then((clientList) => {
      for (const client of clientList) {
        if (client.url && 'focus' in client) {
          client.navigate(targetURL);
          return client.focus();
        }
      }
      if (self.clients.openWindow) {
        return self.clients.openWindow(targetURL);
      }
    })
  );
});
