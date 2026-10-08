/* shell.js — phone navigation for the sidebar and console shells.
   First-party, dependency-free, CSP-clean, and optional: delete it (and
   its <script> tag) and every page still works, because the index and
   the back control are ordinary server-rendered pages and links. It is
   app-owned from the moment it is scaffolded.

   What it reads, all written by the layouts, in either spelling:
     [rst-shell-sidebar~="index"|"page"], [rst-shell-console~=…]
                           which view the server rendered (the "view"
                           block): .rst-shell-sidebar--index and so on
     [rst-shell-back] > a  the back control, the strip's own link; its
                           href is the "up" block. Only the direct child:
                           a page may put a menu in the strip too, and
                           its links are not a way back
     [rst-shell-nav] a     the index's rows

   What it adds, three things:
   1. Direction. In pagereveal it types the view transition "back" or
      "forward", which shell.css turns into a slide.
   2. Back reuses history. A plain click on the back control calls
      history.back() when, and only when, the Navigation API proves the
      entry behind this one is the up page, in another document; the
      index then comes back from the back/forward cache as it was left.
   3. Focus returns to the section you left. Leaving a content page
      records its path in sessionStorage; revealing the index at phone
      width focuses the nav link to that path, or else the one the URL's
      #fragment names.

   The layouts load it with blocking="render": the direction is set in
   pagereveal, at the first render, and a deferred script can run after
   it. An engine that ignores blocking gets an untyped transition, which
   shell.css draws as a cross-fade. */
(function () {
  "use strict";

  var ROOT = "[rst-shell-sidebar],[rst-shell-console],.rst-shell-sidebar,.rst-shell-console";
  var INDEX = '[rst-shell-sidebar~="index"],[rst-shell-console~="index"],.rst-shell-sidebar--index,.rst-shell-console--index';
  var PAGE = '[rst-shell-sidebar~="page"],[rst-shell-console~="page"],.rst-shell-sidebar--page,.rst-shell-console--page';
  var BACK = "[rst-shell-back] > a[href],.rst-shell__back > a[href]";
  var NAV = "[rst-shell-nav] a[href],.rst-shell__nav a[href]";
  var NARROW = "(max-width: 799.98px)";
  var RETURN = "rst-shell-return", WENT_BACK = "rst-shell-back";

  // sessionStorage can throw: storage disabled, a quota, a sandboxed
  // frame. Any throw reads as "no record", so the fragment rule below
  // still works.
  function store(fn) {
    try { return fn(window.sessionStorage); } catch (e) { return null; }
  }
  // One-shot: read and remove, so a record is consumed at most once.
  function take(key) {
    return store(function (s) { var v = s.getItem(key); s.removeItem(key); return v; });
  }
  // A URL as this script compares URLs: resolved against the page, path
  // and query, fragment ignored; null for another origin.
  function place(href) {
    var u = new URL(href, location.href);
    return u.origin === location.origin ? u.pathname + u.search : null;
  }
  function view(sel) {
    var r = document.querySelector(ROOT);
    return !!r && r.matches(sel);
  }

  // 3. Focus return, read when an index is revealed and never while
  // prerendering: a prerendered index runs its first scripts with a copy
  // of storage taken before the reader left the page, and pagereveal at
  // activation is the first moment the fresh record is there. The record
  // wins over the fragment: after going back to /#nav-invoices and then
  // visiting Orders, a history return restores the old fragment. The
  // record is taken before the width test: an index revealed wide would
  // otherwise leave it behind, and a later phone-width index reached some
  // other way would focus a link the reader left long ago.
  function returnFocus() {
    if (document.prerendering || !view(INDEX)) return;
    var rec = take(RETURN), links, hit = null, i, id;
    if (!matchMedia(NARROW).matches) return;
    links = document.querySelectorAll(NAV);
    for (i = 0; rec && !hit && i < links.length; i++) {
      if (place(links[i].href) === rec) hit = links[i];
    }
    if (!hit && location.hash) {
      try { id = decodeURIComponent(location.hash.slice(1)); } catch (e) { id = location.hash.slice(1); }
      hit = document.getElementById(id);
      if (hit && !hit.matches(NAV)) hit = null;
    }
    if (hit) hit.focus();
  }

  // 1. Direction. Back when the page being left recorded that its back
  // control was used AND this page is where it pointed, or when the
  // Navigation API reports a traverse to an earlier entry (the browser's
  // own Back); forward otherwise. The record is written on the click,
  // before anything commits, so a Back whose leave-page prompt was
  // cancelled leaves it behind: scoped to its destination, the next link
  // followed still slides forward. Taken on every reveal, matched or not,
  // so a stale one never waits for its URL to come round. A URL pattern
  // cannot decide it: the new page cannot know which URL is the index,
  // and the pattern is the app's.
  function reveal(e) {
    var back = take(WENT_BACK) === location.pathname + location.search, a = window.navigation && navigation.activation;
    if (!back && a && a.navigationType === "traverse" && a.from && a.entry) back = a.entry.index < a.from.index;
    // Focus first, and the type only where there is a set to add it to:
    // an engine with cross-document transitions but no types must not
    // turn a cosmetic step into a throw that skips the focus return.
    returnFocus();
    if (e.viewTransition && e.viewTransition.types) e.viewTransition.types.add(back ? "back" : "forward");
  }
  if ("onpagereveal" in window) window.addEventListener("pagereveal", reveal);
  else window.addEventListener("pageshow", returnFocus);

  // The record is written when a content page is LEFT, which covers
  // every way back: the back control, the browser's Back, a deep link.
  // pageswap fires on the outgoing document for every navigation, the
  // activation of a prerendered page included, before the new one is
  // revealed; pagehide is the fallback where there is no pageswap.
  // Never both: a pagehide landing after the index took the record
  // would leave a stale one behind.
  function leave() {
    if (view(PAGE)) store(function (s) { s.setItem(RETURN, location.pathname + location.search); });
  }
  window.addEventListener("onpageswap" in window ? "pageswap" : "pagehide", leave);

  // 2. Back reuses history, only when it can prove what is behind: a
  // plain primary click, the Navigation API, and the entry before this
  // one a DIFFERENT document whose path and query are the back link's
  // (index, page, #section on the same page: Back must not stop at the
  // fragment). A redirect behind "up" fails the test and the link is
  // followed. Anything unproven follows the link and history grows by
  // one, which is the scriptless behaviour, not a failure. The referrer
  // is not consulted: it names the document's referrer, not the entry
  // behind this one.
  document.addEventListener("click", function (e) {
    var a = e.target.closest && e.target.closest(BACK), nav = window.navigation, cur, prev, want;
    if (!a || e.defaultPrevented || e.button !== 0 || e.ctrlKey || e.metaKey || e.shiftKey || e.altKey) return;
    want = place(a.href);
    if (want !== null) store(function (s) { s.setItem(WENT_BACK, want); });
    cur = nav && nav.currentEntry;
    if (!cur || cur.index < 1) return;
    prev = nav.entries()[cur.index - 1];
    if (want !== null && prev && !prev.sameDocument && prev.url && place(prev.url) === want) {
      e.preventDefault();
      history.back();
    }
  });
})();
