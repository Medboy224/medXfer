// The portal (WP-W.2 of the J7 plan, bible §11.4). Rules that keep it safe:
// - the token comes in the URL's fragment, which the browser never sends; it is taken out of the
//   address bar at once, kept for this tab only, and sent in Authorization only (WEB-02);
// - no data is ever written as HTML: textContent and createElement only (#157, 3), and no
//   inline script or style, which the CSP forbids anyway (WEB-05).
'use strict';

(function () {
  const TEXTS = {
    fr: {
      pinTitle: 'Tapez le code',
      pinHint: 'Le code à 6 chiffres est affiché sur l’appareil qui partage.',
      pinOk: 'Valider',
      pinWrong: 'Code incorrect. Encore {0} essai(s).',
      pinFormat: 'Le code a 6 chiffres.',
      closedTitle: 'Le partage est terminé',
      closedHint: 'Demandez un nouveau code QR à la personne qui partage.',
      tabReceive: 'Recevoir',
      tabSend: 'Envoyer',
      itemsEmpty: 'Rien n’est partagé pour l’instant.',
      folder: 'dossier',
      download: 'Télécharger',
      sendOff: 'Cet appareil ne reçoit pas de fichiers.',
      choose: 'Choisir des fichiers',
      send: 'Envoyer',
      cancel: 'Annuler',
      waiting: 'En attente de l’accord de {0}…',
      refused: '{0} a refusé ces fichiers.',
      sending: '{0} sur {1} · {2}/s',
      sent: 'Envoyé.',
      cancelled: 'Envoi annulé.',
      busy: 'Un autre envoi est en cours. Réessayez dans un moment.',
      quota: 'C’est plus que ce que ce partage peut recevoir.',
      noSpace: 'L’appareil qui partage n’a pas assez de place.',
      failed: 'L’envoi a échoué. Réessayez.',
      lost: 'Connexion perdue, nouvel essai…',
      expires: 'Partage valable jusqu’à {0}.',
      units: ['o', 'Ko', 'Mo', 'Go', 'To'],
    },
    en: {
      pinTitle: 'Type the code',
      pinHint: 'The 6-digit code is shown on the device that shares.',
      pinOk: 'OK',
      pinWrong: 'Wrong code. {0} try(ies) left.',
      pinFormat: 'The code has 6 digits.',
      closedTitle: 'Sharing has ended',
      closedHint: 'Ask the person who shares for a new QR code.',
      tabReceive: 'Receive',
      tabSend: 'Send',
      itemsEmpty: 'Nothing is shared yet.',
      folder: 'folder',
      download: 'Download',
      sendOff: 'This device does not receive files.',
      choose: 'Choose files',
      send: 'Send',
      cancel: 'Cancel',
      waiting: 'Waiting for {0} to accept…',
      refused: '{0} refused these files.',
      sending: '{0} of {1} · {2}/s',
      sent: 'Sent.',
      cancelled: 'Sending cancelled.',
      busy: 'Another transfer is running. Try again in a moment.',
      quota: 'That is more than this share can receive.',
      noSpace: 'The device that shares does not have enough space.',
      failed: 'Sending failed. Try again.',
      lost: 'Connection lost, trying again…',
      expires: 'Sharing open until {0}.',
      units: ['B', 'KB', 'MB', 'GB', 'TB'],
    },
  };
  const lang = (navigator.language || 'en').toLowerCase().startsWith('fr') ? 'fr' : 'en';
  const L = TEXTS[lang];
  const t = (key, ...args) => args.reduce((s, v, i) => s.replace('{' + i + '}', String(v)), L[key] || key);
  document.documentElement.lang = lang;
  document.querySelectorAll('[data-i18n]').forEach((el) => { el.textContent = t(el.dataset.i18n); });

  const $ = (id) => document.getElementById(id);
  const KEY = 'medxfer-token';
  let token = null;
  let hostName = '';

  // The token: from the fragment, then out of the address bar; this tab keeps it for a reload.
  const found = /(?:^|&)t=([0-9a-f]{32})(?:&|$)/.exec(location.hash.slice(1));
  if (location.hash) history.replaceState(null, '', location.pathname);
  try {
    if (found) sessionStorage.setItem(KEY, found[1]);
    token = sessionStorage.getItem(KEY);
  } catch (e) {
    token = found ? found[1] : null;
  }

  function forget() {
    token = null;
    try { sessionStorage.removeItem(KEY); } catch (e) { /* storage unavailable */ }
  }

  function show(id) {
    for (const s of ['pin', 'closed', 'main']) $(s).hidden = s !== id;
  }

  // An answer that ends what the page was doing: the session gone, or the token refused.
  class Stop extends Error {}

  function ended() {
    forget();
    show('closed');
    $('expires').textContent = '';
  }

  async function api(method, path, body, raw) {
    const opts = { method, headers: {} };
    if (token) opts.headers.Authorization = 'Bearer ' + token;
    if (body !== undefined) {
      if (raw) {
        opts.body = body;
      } else {
        opts.headers['Content-Type'] = 'application/json';
        opts.body = JSON.stringify(body);
      }
    }
    const r = await fetch(path, opts);
    let data = {};
    try { data = await r.json(); } catch (e) { /* no JSON */ }
    if (r.status === 410) { ended(); throw new Stop(); }
    if (r.status === 401 && path !== '/api/unlock') { forget(); askPIN(); throw new Stop(); }
    return { status: r.status, data };
  }

  function size(n) {
    let i = 0;
    while (n >= 1024 && i < L.units.length - 1) { n /= 1024; i++; }
    return (i === 0 ? String(n) : n.toFixed(n < 10 ? 1 : 0)) + ' ' + L.units[i];
  }

  function el(tag, cls, text) {
    const e = document.createElement(tag);
    if (cls) e.className = cls;
    if (text !== undefined) e.textContent = text;
    return e;
  }

  // The PIN (WEB-03): 6 digits, spaces allowed as the host shows them.
  function askPIN() {
    show('pin');
    $('pin-input').value = '';
    $('pin-input').focus();
  }

  $('pin-form').addEventListener('submit', async (ev) => {
    ev.preventDefault();
    const pin = $('pin-input').value.replace(/\s+/g, '');
    if (!/^\d{6}$/.test(pin)) { $('pin-error').textContent = t('pinFormat'); return; }
    $('pin-ok').disabled = true;
    try {
      const { status, data } = await api('POST', '/api/unlock', { pin });
      if (status === 200 && data.token) {
        token = data.token;
        try { sessionStorage.setItem(KEY, token); } catch (e) { /* this load only */ }
        $('pin-error').textContent = '';
        await start();
      } else {
        $('pin-error').textContent = data.tries_left !== undefined ? t('pinWrong', data.tries_left) : t('failed');
        $('pin-input').select();
      }
    } catch (e) {
      if (!(e instanceof Stop)) $('pin-error').textContent = t('failed');
    } finally {
      $('pin-ok').disabled = false;
    }
  });

  // The tabs.
  function tab(name) {
    $('receive').hidden = name !== 'receive';
    $('send').hidden = name !== 'send';
    $('tab-receive').setAttribute('aria-selected', String(name === 'receive'));
    $('tab-send').setAttribute('aria-selected', String(name === 'send'));
  }
  $('tab-receive').addEventListener('click', () => tab('receive'));
  $('tab-send').addEventListener('click', () => tab('send'));

  // What the host shares, named by ID (WEB-07); saved through a link of its own, the browser
  // writing the file as it comes rather than holding it in memory.
  function renderItems(items) {
    const list = $('items');
    list.replaceChildren();
    $('items-empty').hidden = items.length > 0;
    for (const it of items) {
      const li = el('li');
      const what = el('div', 'what');
      what.append(el('div', 'name', it.name), el('div', 'detail', it.folder ? t('folder') + ' · ' + size(it.size) : size(it.size)));
      const button = el('button', 'primary', t('download'));
      button.type = 'button';
      button.addEventListener('click', async () => {
        button.disabled = true;
        try {
          const { status, data } = await api('POST', '/api/items/' + encodeURIComponent(it.id) + '/link');
          if (status === 200 && typeof data.url === 'string' && data.url.startsWith('/dl/')) {
            const a = el('a');
            a.href = data.url;
            a.rel = 'noreferrer';
            document.body.append(a);
            a.click();
            a.remove();
          }
        } catch (e) { /* said by api */ } finally {
          button.disabled = false;
        }
      });
      li.append(what, button);
      list.append(li);
    }
  }

  function showSession(data) {
    hostName = data.name || 'medXfer';
    $('host').textContent = hostName;
    $('send-off').hidden = !!data.receive;
    $('send-on').hidden = !data.receive;
    if (data.expires) {
      const at = new Date(data.expires);
      $('expires').textContent = t('expires', at.toLocaleTimeString(lang, { hour: '2-digit', minute: '2-digit' }));
    }
  }

  async function start() {
    const session = await api('GET', '/api/session');
    if (session.status !== 200) return;
    showSession(session.data);
    const items = await api('GET', '/api/items');
    renderItems(Array.isArray(items.data) ? items.data : []);
    show('main');
    if (!session.data.receive) tab('receive');
  }

  // Every half minute: the expiry moves while files come, and an ended session is said.
  setInterval(async () => {
    if (!token || $('main').hidden) return;
    try {
      const s = await api('GET', '/api/session');
      if (s.status === 200) showSession(s.data);
    } catch (e) { /* said by api */ }
  }, 30000);

  // Sending (bible §11.4): announce, wait for the host, then each file in chunks, resumed from
  // what the host received after a cut.
  let picked = [];
  let sending = null; // { ticket, stop }

  $('files').addEventListener('change', () => {
    picked = Array.from($('files').files || []);
    const queue = $('queue');
    queue.replaceChildren();
    for (const f of picked) {
      const li = el('li');
      const what = el('div', 'what');
      const bar = el('progress');
      bar.max = Math.max(f.size, 1);
      bar.value = 0;
      what.append(el('div', 'name', f.name), el('div', 'detail', size(f.size)), bar);
      li.append(what);
      queue.append(li);
      f.bar = bar;
      f.li = li;
    }
    $('send-go').disabled = picked.length === 0;
    $('send-status').textContent = '';
  });

  function status(text) { $('send-status').textContent = text; }

  const sleep = (ms) => new Promise((r) => setTimeout(r, ms));

  // One chunk, by XMLHttpRequest rather than fetch: its upload reports the bytes as they leave,
  // the bar moving during a chunk (real test of 10 November 2026: an 8 MiB chunk took seconds
  // on a slow network, and the bar waited for it). onSent gets the bytes of this chunk sent.
  function putChunk(path, blob, onSent) {
    return new Promise((resolve, reject) => {
      const x = new XMLHttpRequest();
      x.open('PUT', path);
      x.setRequestHeader('Authorization', 'Bearer ' + token);
      x.upload.onprogress = (ev) => onSent(ev.loaded);
      x.onerror = () => reject(new Error('network'));
      x.ontimeout = () => reject(new Error('network'));
      x.onload = () => {
        let data = {};
        try { data = JSON.parse(x.responseText); } catch (e) { /* no JSON */ }
        if (x.status === 410) { ended(); reject(new Stop()); return; }
        if (x.status === 401) { forget(); askPIN(); reject(new Stop()); return; }
        resolve({ status: x.status, data });
      };
      x.send(blob);
    });
  }

  async function state(ticket) {
    const { status: code, data } = await api('GET', '/api/uploads/' + ticket);
    return code === 200 ? data : null;
  }

  $('send-go').addEventListener('click', async () => {
    if (sending || picked.length === 0) return;
    const files = picked;
    $('send-go').disabled = true;
    $('files').disabled = true;
    try {
      const { status: code, data } = await api('POST', '/api/uploads', { files: files.map((f) => ({ name: f.name, size: f.size })) });
      if (code !== 201) {
        status(t({ 409: 'busy', 413: 'quota', 507: 'noSpace' }[code] || 'failed'));
        return;
      }
      sending = { ticket: data.ticket, stop: false };
      $('send-cancel').hidden = false;
      const chunk = data.chunk || 8 << 20;
      let st = data.state;
      while (st === 'pending' && !sending.stop) {
        status(t('waiting', hostName));
        await sleep(1000);
        const s = await state(sending.ticket);
        st = s ? s.state : 'failed';
      }
      if (sending.stop) return;
      if (st === 'refused') { status(t('refused', hostName)); return; }
      if (st !== 'accepted' && st !== 'done') { status(t('failed')); return; }

      const total = files.reduce((n, f) => n + f.size, 0);
      let done = 0;
      const began = Date.now();
      let shownAt = 0;
      const progress = (moving) => {
        const now = Date.now();
        if (moving && now - shownAt < 250) return; // 4 times a second at most
        shownAt = now;
        const secs = Math.max((now - began) / 1000, 0.5);
        status(t('sending', size(moving), size(total), size(Math.round(moving / secs))));
      };
      for (let i = 0; i < files.length && !sending.stop; i++) {
        const f = files[i];
        let offset = 0;
        let misses = 0;
        while (offset < f.size && !sending.stop) {
          let r;
          const base = offset;
          try {
            r = await putChunk('/api/uploads/' + sending.ticket + '/' + i + '?offset=' + offset, f.slice(offset, offset + chunk), (sent) => {
              f.bar.value = base + sent;
              progress(done + sent);
            });
          } catch (e) {
            if (e instanceof Stop) return;
            r = null; // the network: asked again below
          }
          if (r && r.status === 200) {
            done += r.data.received - offset;
            offset = r.data.received;
            misses = 0;
          } else if (r && r.status === 409 && r.data.error === 'offset') {
            done += r.data.received - offset;
            offset = r.data.received;
          } else if (r && r.status === 409) {
            status(t('cancelled')); // the host cancelled
            return;
          } else {
            if (++misses > 5) { status(t('failed')); return; }
            status(t('lost'));
            await sleep(1000 * misses);
            const s = await state(sending.ticket).catch(() => null);
            if (s && s.files && s.files[i]) {
              done += s.files[i].received - offset;
              offset = s.files[i].received;
            }
            continue;
          }
          f.bar.value = offset;
          progress(done);
        }
        f.bar.value = f.size;
        f.li.classList.add('done');
      }
      if (!sending.stop) {
        status(t('sent'));
        $('files').value = '';
        picked = [];
      }
    } catch (e) {
      if (!(e instanceof Stop)) status(t('failed'));
    } finally {
      sending = null;
      $('send-cancel').hidden = true;
      $('files').disabled = false;
      $('send-go').disabled = picked.length === 0;
    }
  });

  $('send-cancel').addEventListener('click', async () => {
    if (!sending) return;
    sending.stop = true;
    try { await api('DELETE', '/api/uploads/' + sending.ticket); } catch (e) { /* said by api */ }
    status(t('cancelled'));
  });

  // The first screen: the shares with a token, the PIN without.
  if (token) {
    start().catch((e) => { if (!(e instanceof Stop)) askPIN(); });
  } else {
    askPIN();
  }
})();
