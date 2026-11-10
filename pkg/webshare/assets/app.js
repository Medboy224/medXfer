// The portal's script (WP-W.2 of the J7 plan writes the page). The token comes in the URL's
// fragment, which the browser never sends; it is taken out of the address bar at once, then
// sent in Authorization only (WEB-02).
'use strict';

(function () {
  const match = /(?:^|&)t=([0-9a-f]{32})/.exec(location.hash.slice(1));
  if (match) history.replaceState(null, '', location.pathname);
  const status = document.getElementById('status');
  status.textContent = match ? 'Connecté.' : 'Tapez le PIN affiché sur l’autre appareil.';
})();
