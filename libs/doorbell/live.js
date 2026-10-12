/* live.js: a page's doorbell client (libs/doorbell, ADR 0004). An app vendors it as
   static/live.js and loads it with data-events (the stream's URL) and data-head (the
   latest change id the page was rendered at). One EventSource per page; each "update"
   re-fetches every [data-live] element's fragment route (Rastrillo-Fragment: 1) and swaps
   it in, 150ms debounced. Not rastrillo.js's data-poll-push: that opens one stream per
   element (past HTTP/1.1's six per origin with a few tabs) and drops to timer polling for
   good on any error, the server's 5-minute close included, so Last-Event-ID never
   resumes. An element being edited (focus or a changed field inside) is left alone and
   refreshed once it is not. Each update is also told to the page as a live:update event
   on document (detail: the head), so a script island re-reads its data on the same one
   stream. Without JavaScript the page is a plain page: reload it to see changes. */
(function () {
  "use strict";
  var me = document.currentScript, url = me && me.getAttribute("data-events");
  if (!url || !window.EventSource || !window.fetch) return;
  var head = me.getAttribute("data-head") || "0", timer = 0, seq = 0;
  function busy(el) {
    var a = document.activeElement;
    if (a && a !== document.body && el.contains(a) && a.matches("input,textarea,select")) return true;
    return [].some.call(el.querySelectorAll("textarea,input:not([type=hidden])"), function (f) {
      return /^(checkbox|radio)$/.test(f.type) ? f.checked !== f.defaultChecked : f.value !== f.defaultValue;
    });
  }
  function refresh(el) {
    if (busy(el)) { el.setAttribute("data-live-stale", ""); return; }
    var mine = el.liveSeq = ++seq;
    fetch(el.getAttribute("data-live"), { headers: { "Rastrillo-Fragment": "1" }, credentials: "same-origin" })
      .then(function (res) { return res.ok ? res.text() : null; })
      .then(function (html) {
        if (html === null || el.liveSeq !== mine) return; // refused, or a later fetch owns it
        if (busy(el)) { el.setAttribute("data-live-stale", ""); return; }
        el.innerHTML = html;
        el.removeAttribute("data-live-stale");
      }, function () {});
  }
  function all() { document.querySelectorAll("[data-live]").forEach(refresh); }
  document.addEventListener("focusout", function (e) {
    var el = e.target.closest && e.target.closest("[data-live-stale]");
    if (el) setTimeout(function () { refresh(el); }, 0);
  });
  function open() {
    var es = new EventSource(url + "?after=" + encodeURIComponent(head));
    es.addEventListener("update", function (e) {
      head = e.lastEventId || head;
      document.dispatchEvent(new CustomEvent("live:update", { detail: head }));
      clearTimeout(timer);
      timer = setTimeout(all, 150);
    });
    // EventSource reconnects by itself (with Last-Event-ID) unless the server refused
    // the stream outright (signed out, 503 when full): then try again, slowly.
    es.onerror = function () { if (es.readyState === EventSource.CLOSED) setTimeout(open, 30000); };
  }
  open();
})();
