import { useCallback, useEffect, useState } from 'react';

function urlBase64ToUint8Array(base64String) {
  const padding = '='.repeat((4 - (base64String.length % 4)) % 4);
  const base64 = (base64String + padding).replace(/-/g, '+').replace(/_/g, '/');
  const raw = atob(base64);
  const out = new Uint8Array(raw.length);
  for (let i = 0; i < raw.length; i++) out[i] = raw.charCodeAt(i);
  return out;
}

const hasWindow = typeof window !== 'undefined';

export function usePush() {
  const isSecure = hasWindow ? !!window.isSecureContext : false;
  const hasSW = hasWindow && 'serviceWorker' in navigator;
  const hasPush = hasWindow && 'PushManager' in window;
  const hasNotif = hasWindow && 'Notification' in window;
  const supported = isSecure && hasSW && hasPush && hasNotif;

  const [permission, setPermission] = useState(hasNotif ? Notification.permission : 'denied');
  const [subscribed, setSubscribed] = useState(false);
  const [swState, setSwState] = useState('unknown'); // pending | ready | timeout | error | n/a
  const [lastError, setLastError] = useState('');

  useEffect(() => {
    if (!hasSW) {
      setSwState('n/a');
      return;
    }
    setSwState('pending');
    // navigator.serviceWorker.ready hangs forever if registration failed, so race it.
    const timeout = new Promise((_, reject) =>
      setTimeout(() => reject(new Error('service worker timeout (likely cert/secure-context issue)')), 4000)
    );
    Promise.race([navigator.serviceWorker.ready, timeout])
      .then((reg) => {
        setSwState('ready');
        return reg.pushManager.getSubscription();
      })
      .then(async (sub) => {
        if (!sub) {
          setSubscribed(false);
          return;
        }
        // Browser remembered an existing subscription. Re-register it with the
        // server in case aurex was restarted and lost its in-memory list. The
        // server dedupes by endpoint so this is safe to call every load.
        try {
          const res = await fetch('/api/push/subscribe', {
            method: 'POST',
            headers: { 'Content-Type': 'application/json' },
            body: JSON.stringify(sub),
          });
          if (!res.ok) {
            const text = await res.text().catch(() => '');
            setLastError(`re-register failed: ${res.status} ${text}`);
          }
        } catch (err) {
          setLastError(`re-register error: ${err?.message || err}`);
        }
        setSubscribed(true);
      })
      .catch((err) => {
        setSwState(err.message.includes('timeout') ? 'timeout' : 'error');
        setLastError(String(err.message || err));
      });
  }, [hasSW]);

  const subscribe = useCallback(async () => {
    setLastError('');
    if (!hasNotif) {
      setLastError('Notification API unavailable in this browser');
      return false;
    }
    if (!isSecure) {
      setLastError('Page is not a secure context. Chrome flag or installed cert required.');
      return false;
    }
    try {
      const perm = await Notification.requestPermission();
      setPermission(perm);
      if (perm !== 'granted') {
        setLastError(`Permission ${perm}`);
        return false;
      }

      const res = await fetch('/api/push/vapid-public-key');
      const { publicKey } = await res.json();
      if (!publicKey) {
        setLastError('Server returned no VAPID key');
        return false;
      }

      const reg = await navigator.serviceWorker.ready;
      // Force a fresh subscription. If a previous subscription exists bound
      // to a different VAPID public key (e.g. aurex.config.json was regenerated
      // at some point), Chrome will silently return the stale one and FCM
      // will reject every push with 403. Unsubscribing first guarantees the
      // new subscribe() call mints a subscription tied to the CURRENT key.
      const stale = await reg.pushManager.getSubscription();
      if (stale) {
        await stale.unsubscribe().catch(() => {});
      }
      const sub = await reg.pushManager.subscribe({
        userVisibleOnly: true,
        applicationServerKey: urlBase64ToUint8Array(publicKey),
      });
      const postRes = await fetch('/api/push/subscribe', {
        method: 'POST',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify(sub),
      });
      if (!postRes.ok) {
        const text = await postRes.text().catch(() => '');
        setLastError(`server rejected subscription: ${postRes.status} ${text}`);
        return false;
      }
      setSubscribed(true);
      return true;
    } catch (err) {
      setLastError(String(err?.message || err));
      return false;
    }
  }, [hasNotif, isSecure]);

  const unsubscribe = useCallback(async () => {
    if (!hasSW) return false;
    try {
      const reg = await navigator.serviceWorker.ready;
      const sub = await reg.pushManager.getSubscription();
      if (sub) {
        await sub.unsubscribe();
      }
      setSubscribed(false);
      setLastError('');
      return true;
    } catch (err) {
      setLastError(String(err?.message || err));
      return false;
    }
  }, [hasSW]);

  return {
    supported,
    permission,
    subscribed,
    subscribe,
    unsubscribe,
    isSecure,
    hasSW,
    hasPush,
    hasNotif,
    swState,
    lastError,
  };
}
