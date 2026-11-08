/* Atelier Bambu Lab - cérémonie WebAuthn des pages humaines (/setup/passkeys, /confirm, /stop).
 * Script statique servi par bambud (même origine, CSP script-src 'self') : aucun JavaScript en ligne.
 * Il ne fait que : lire les options données par le serveur (attribut data-pk-options du formulaire), appeler
 * navigator.credentials.create / get, puis remplir deux champs cachés et soumettre le formulaire (navigation de même
 * origine : mêmes protections que les autres formulaires). Aucun secret ne passe ici : la clé privée reste dans
 * l'authentificateur ; le serveur vérifie tout (signature, origine, challenge, compteur). */
(function () {
  'use strict';

  function dec(s) {
    s = String(s).replace(/-/g, '+').replace(/_/g, '/');
    while (s.length % 4) { s += '='; }
    var bin = atob(s), a = new Uint8Array(bin.length);
    for (var i = 0; i < bin.length; i++) { a[i] = bin.charCodeAt(i); }
    return a.buffer;
  }
  function enc(buf) {
    var a = new Uint8Array(buf), s = '';
    for (var i = 0; i < a.length; i++) { s += String.fromCharCode(a[i]); }
    return btoa(s).replace(/\+/g, '-').replace(/\//g, '_').replace(/=+$/, '');
  }
  function say(text, bad) {
    var m = document.getElementById('pk-msg');
    if (!m) { return; }
    m.textContent = text;
    m.className = bad ? 'warn' : 'ok';
    m.hidden = !text;
  }
  function explain(e) {
    if (e && e.name === 'NotAllowedError') { return 'La passkey a été refusée ou annulée (ou le délai est dépassé).'; }
    if (e && e.name === 'InvalidStateError') { return 'Cette passkey est déjà enregistrée sur cet appareil.'; }
    if (e && e.name === 'SecurityError') { return 'Le navigateur refuse : adresse non sécurisée ou différente de celle déclarée (BAMBU_PUBLIC_URL, https).'; }
    return 'La passkey n\'a pas pu être utilisée (' + (e && e.name ? e.name : 'erreur') + ').';
  }
  function creds(list) {
    return (list || []).map(function (c) {
      var o = { type: c.type, id: dec(c.id) };
      if (c.transports) { o.transports = c.transports; }
      return o;
    });
  }
  function createOptions(pk) {
    pk.challenge = dec(pk.challenge);
    pk.user.id = dec(pk.user.id);
    pk.excludeCredentials = creds(pk.excludeCredentials);
    return pk;
  }
  function getOptions(pk) {
    pk.challenge = dec(pk.challenge);
    pk.allowCredentials = creds(pk.allowCredentials);
    return pk;
  }
  function ext(c) {
    try { return JSON.parse(JSON.stringify(c.getClientExtensionResults ? c.getClientExtensionResults() : {})); } catch (e) { return {}; }
  }
  function creationJSON(c) {
    var r = c.response, out = {
      id: c.id, rawId: enc(c.rawId), type: c.type,
      response: { clientDataJSON: enc(r.clientDataJSON), attestationObject: enc(r.attestationObject) },
      clientExtensionResults: ext(c)
    };
    if (c.authenticatorAttachment) { out.authenticatorAttachment = c.authenticatorAttachment; }
    if (r.getTransports) { try { out.response.transports = r.getTransports(); } catch (e) { /* facultatif */ } }
    return JSON.stringify(out);
  }
  function assertionJSON(c) {
    var r = c.response, out = {
      id: c.id, rawId: enc(c.rawId), type: c.type,
      response: { clientDataJSON: enc(r.clientDataJSON), authenticatorData: enc(r.authenticatorData), signature: enc(r.signature) },
      clientExtensionResults: ext(c)
    };
    if (r.userHandle) { out.response.userHandle = enc(r.userHandle); }
    if (c.authenticatorAttachment) { out.authenticatorAttachment = c.authenticatorAttachment; }
    return JSON.stringify(out);
  }
  function field(form, name, value) {
    var i = form.querySelector('input[name="' + name + '"]');
    if (!i) { i = document.createElement('input'); i.type = 'hidden'; i.name = name; form.appendChild(i); }
    i.value = value;
  }

  function run(form, btn) {
    var raw = form.getAttribute('data-pk-options');
    var mode = form.getAttribute('data-pk-mode');
    if (!raw || !mode) { say('Aucune option de passkey : recharge la page.', true); return; }
    if (typeof form.reportValidity === 'function' && !form.reportValidity()) { return; }
    var pk;
    try { pk = JSON.parse(raw).publicKey; } catch (e) { say('Options de passkey illisibles : recharge la page.', true); return; }
    btn.disabled = true;
    say('Valide avec ta passkey (empreinte, visage, code de l\'appareil ou clé de sécurité)...', false);
    var p = mode === 'create'
      ? navigator.credentials.create({ publicKey: createOptions(pk) }).then(function (c) { field(form, 'credential', creationJSON(c)); })
      : navigator.credentials.get({ publicKey: getOptions(pk) }).then(function (c) { field(form, 'assertion', assertionJSON(c)); });
    p.then(function () {
      field(form, 'method', 'passkey');
      form.submit(); // navigation de même origine : mêmes en-têtes Sec-Fetch que les autres formulaires
    }, function (e) {
      btn.disabled = false;
      say(explain(e), true);
    });
  }

  function init() {
    var ok = !!(window.PublicKeyCredential && navigator.credentials && navigator.credentials.create && navigator.credentials.get);
    var buttons = document.querySelectorAll('[data-pk-go]');
    for (var i = 0; i < buttons.length; i++) {
      (function (btn) {
        var form = btn.form || btn.closest('form');
        if (!form) { return; }
        if (!ok) { say('Ce navigateur ne gère pas les passkeys (WebAuthn).', true); return; }
        btn.hidden = false;
        btn.addEventListener('click', function (ev) { ev.preventDefault(); run(form, btn); });
      })(buttons[i]);
    }
    var fallback = document.querySelectorAll('[data-pk-fallback]');
    for (var j = 0; j < fallback.length; j++) { fallback[j].hidden = ok; }
  }
  if (document.readyState === 'loading') { document.addEventListener('DOMContentLoaded', init); } else { init(); }
})();
