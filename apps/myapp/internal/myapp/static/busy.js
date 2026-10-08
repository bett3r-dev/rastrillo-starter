/* busy.js — the busy rule. First-party, dependency-free, and on by
   default: link it and every submit button in every form that goes
   somewhere is covered, with no attribute to remember, because a button
   that changes something should say so while it works, and a form that
   double-submits on a second click is a bug on every screen. It is
   app-owned from the moment it is vendored; leave it unlinked to opt the
   whole app out.

   What a submitted button does: its label gives way to a spinner (the
   label stays, beside the spinner, under reduced motion or forced
   colours — tokens.css), it goes aria-busy and then disabled, and its
   form refuses a second submit. The spinner shows for at least
   HOLD_MS: a response faster than that is held back until then, so a
   quick save reads as a deliberate beat rather than a flicker. Going
   Back to the page hands every busy form back, and a submit still being
   held when the page is left is dropped rather than sent later. Left
   means a navigation has started, not that it has finished. A form
   pow.js protects is pow's to hold: this file arms only pow's re-send
   and never holds it.

   Vocabulary:
     data-busy="false"     on a <form> or on one submit button: opt OUT
                           of the busy rule. The rule is the default, so
                           any other value — data-busy on its own
                           included — changes nothing
     data-busy-label="…"   on the form or on the button: replacement
                           button text while it works

   A script that handles a submit itself (a fetch, say) cancels it in a
   listener on the form or the document, as it would anyway: those run
   before this file's window listener, which then sees the cancelled
   submit and leaves it — and its feedback — to that script. Two edges
   of the hold, on purpose: a handler that stops the submit event's
   propagation keeps it from the window, so that submit goes at once,
   unheld; and a form removed from the page during the hold (a polled
   fragment replacing it) takes its held submit with it. */
(function () {
  "use strict";

  // How long the spinner shows, at least. Long enough to read as an
  // answer to the click, short enough never to feel like waiting.
  var HOLD_MS = 650;

  // ── The busy rule ────────────────────────────────────────────────
  //
  // A button that CHANGES something says so while it works; a button
  // that only reveals something — a disclosure, a dropdown, a tab —
  // does not. So every submit button in every form is covered by
  // default, with no attribute to remember: the submitted button gets
  // aria-busy, a spinner and — once the submission is under way —
  // disabled, and its form gets aria-busy and a guard against a second
  // submit. The data-busy vocabulary above opts out of either half.
  //
  // The guard is the substance, the spinner is the manners, and neither
  // is a promise: with scripts off the form submits exactly as it
  // always did, twice if the visitor clicks twice. Idempotency stays
  // the server's job. One delegated capture-phase listener, for the
  // reasons light dismiss gives below: fragments, and never double-bind.
  //
  // Two traps this shape exists to avoid:
  //
  //   - A form the browser REFUSED must not sit there looking busy.
  //     Constraint validation fails before the submit event is fired at
  //     all, so an invalid form never reaches this code; a cancelled
  //     one is handed back in the tick below. (formnovalidate skips
  //     validation and really does submit, which should look like it.)
  //   - Nothing that could change the payload happens while the payload
  //     is being read. The entry list is built before submit fires, but
  //     engines have differed, so aria-busy, the spinner and a
  //     <button>'s TEXT (never submitted — a button submits its value
  //     attribute) go on synchronously, while disabled and an
  //     <input type="submit">'s value (which IS what it submits) wait.
  //
  // Every busy element in the DOCUMENT, then the ownership test: a
  // form="id" submit button is owned by a form it does not live in.
  function busyOff(form) {
    form.removeAttribute("aria-busy");
    document.querySelectorAll('[aria-busy="true"]').forEach(function (b) {
      if (b.form !== form && !form.contains(b)) return;
      b.disabled = false;
      b.removeAttribute("aria-busy");
      var spin = b.querySelector("[rst-spin]");
      if (spin) spin.remove();
      var idle = b.getAttribute("data-idle-label");
      if (idle === null) return;
      if (b.tagName === "INPUT") { b.value = idle; } else { b.textContent = idle; }
      b.removeAttribute("data-idle-label");
    });
  }

  function busySubmit(e) {
    var form = e.target;
    if (!form || form.tagName !== "FORM") return;
    // pow.js owns every submit of a form it protects: it cancels it,
    // shows its own busy state (this file's vocabulary) while it waits,
    // and re-sends it marked data-pow-released. Arming here before that
    // would hand the form back a tick later and wipe pow's feedback, and
    // a guard armed here would refuse pow's re-send. Arm on the re-send
    // only.
    if (form.hasAttribute("data-pow-form") && !form.hasAttribute("data-pow-released")) return;
    if (form.getAttribute("data-busy") === "false") return;
    // The button the browser submitted with: the one clicked, or — for
    // Enter in a field — the default one it implicitly clicked. Only
    // that one goes busy; every other submit button in the form keeps
    // its name and its value. An engine with no SubmitEvent.submitter
    // leaves the buttons alone and keeps the guard, which is the half
    // that matters.
    var btn = e.submitter;
    // A form that opens its result elsewhere, or a submit that closes a
    // dialog and stays put, has nothing to be busy about and nothing to
    // clear. The submitter's formmethod beats the form's.
    //
    // getAttribute, never the IDL properties. A form is
    // [LegacyOverrideBuiltIns]: a control named "target" or "method" (a
    // target date, a method column — ordinary field names) shadows the IDL
    // attribute with the input itself, so the property form quietly
    // switches the rule off and hands back the double submit. The busy flag
    // below is the form's own aria-busy for the same reason: an expando is
    // shadowable, and under strict mode assigning to a shadowed one throws.
    var to = (btn && btn.getAttribute("formtarget")) || form.getAttribute("target");
    if (to && to !== "_self") return;
    if (/^dialog$/i.test(btn && btn.getAttribute("formmethod") ||
      form.getAttribute("method"))) return;
    if (form === releasing) return; // the hold sending the submit it held
    if (form.getAttribute("aria-busy") === "true") { e.preventDefault(); return; }
    form.setAttribute("aria-busy", "true");
    if (!btn || btn.getAttribute("data-busy") === "false") btn = null;
    var label = btn && (btn.getAttribute("data-busy-label") ||
      form.getAttribute("data-busy-label"));
    if (btn) {
      btn.setAttribute("aria-busy", "true");
      if (btn.tagName === "BUTTON") {
        if (label) {
          btn.setAttribute("data-idle-label", btn.textContent);
          btn.textContent = label;
        }
        // A child element, not a pseudo-element: the shim has to be
        // able to take it away again. An <input type="submit"> has no
        // children, which is the one shape this cannot draw a spinner
        // on. tokens.css stops it rotating under reduced motion.
        var spin = document.createElement("span");
        spin.setAttribute("rst-spin", "");
        spin.setAttribute("aria-hidden", "true");
        btn.insertBefore(spin, btn.firstChild);
      }
    }
    setTimeout(function () {
      // Someone downstream cancelled the submit — an app handler doing
      // the work itself, most likely. Nothing is on its way anywhere,
      // so hand the form back, guard included, and let whoever took the
      // job own the feedback too.
      if (e.defaultPrevented && !held.has(e)) { busyOff(form); return; }
      if (!btn) return;
      if (label && btn.tagName === "INPUT") {
        btn.setAttribute("data-idle-label", btn.value);
        btn.value = label;
      }
      btn.disabled = true;
    }, 0);
  }

  document.addEventListener("submit", busySubmit, true);

  // ── The hold ─────────────────────────────────────────────────────
  //
  // A spinner can only be promised a minimum time by holding the
  // submit itself: once a fast response arrives the browser navigates
  // and the spinner is gone. So the submit the busy rule just armed is
  // cancelled here and sent again, by requestSubmit, once HOLD_MS have
  // passed since the click.
  //
  // This listener is on the window, in the bubble phase — the last place
  // a submit event reaches — so every handler on the form and the
  // document has had its say first. One that cancelled the submit owns
  // it (e.defaultPrevented, and the busy tick above hands the form
  // back); so does the guard's own refusal of a second submit. Only a
  // submit nobody else stopped is held, and only if the busy rule armed
  // it (aria-busy on the form: not opted out, not a dialog, not a new
  // window) and a button is showing the spinner.
  var held = new WeakSet(); // submit events this listener cancelled
  var pending = new Map();  // form -> timer, for a hold still running
  var releasing = null;     // the form whose held submit is going now
  window.addEventListener("submit", function (e) {
    var form = e.target;
    if (!form || form.tagName !== "FORM") return;
    if (form === releasing) { releasing = null; return; }
    // Never hold a pow form: pow has already held it while it solved,
    // and a second hold is a second wait for nothing. Worse, holding it
    // here and re-sending later would send it back through pow, which
    // would hold it again with busy.js's guard still armed, and the form
    // could never submit.
    if (form.hasAttribute("data-pow-form")) return;
    if (e.defaultPrevented || form.getAttribute("aria-busy") !== "true") return;
    // Only a submit with a spinner to show is held: the hold is for the
    // spinner. A script's requestSubmit() with no submitter, or a button
    // that opted out, has nothing on screen to keep up, and holding it
    // would only be a silent 650ms stall. The form is still guarded.
    var btn = e.submitter || null;
    if (!btn || btn.getAttribute("aria-busy") !== "true") return;
    e.preventDefault();
    held.add(e);
    pending.set(form, setTimeout(function () { release(form, btn); }, HOLD_MS));
    // pagehide fires only once a navigation commits, and a slow
    // destination leaves the hold running until then: a held submit
    // released meanwhile replaces the navigation the visitor chose.
    // beforeunload fires as the navigation starts. Attached only while
    // something is held, so it keeps no page out of the back-forward
    // cache.
    if (pending.size === 1) window.addEventListener("beforeunload", dropHeld);
  });

  // Send the held submit. The submitter goes with it, so its name and
  // value are in the payload exactly as a click would have sent them —
  // which means, for the moment the entry list is built, it has to be
  // enabled and an <input type="submit"> has to carry its own value
  // again, not the busy label. The form is re-checked first: fields
  // stay editable during the hold, and a form that is no longer valid
  // must be handed back with its errors showing, not left spinning.
  function release(form, btn) {
    pending.delete(form);
    if (pending.size === 0) window.removeEventListener("beforeunload", dropHeld);
    if (!form.isConnected) return;
    var skip = form.hasAttribute("novalidate") ||
      (btn && btn.hasAttribute("formnovalidate"));
    if (!skip && !form.checkValidity()) {
      busyOff(form);
      form.reportValidity();
      return;
    }
    // The submitter may have left the form during the hold (moved,
    // removed, its form= changed): requestSubmit would throw and leave
    // the form guarded for good. Hand it back instead.
    if (btn && (!btn.isConnected || btn.form !== form)) {
      busyOff(form);
      return;
    }
    var busyValue = null;
    // Restored, not assumed: a button that opted out of the busy state
    // (data-busy="false") was never disabled, and must not end up so.
    var wasDisabled = btn ? btn.disabled : false;
    if (btn) {
      btn.disabled = false;
      var idle = btn.getAttribute("data-idle-label");
      if (btn.tagName === "INPUT" && idle !== null) {
        busyValue = btn.value;
        btn.value = idle;
      }
    }
    releasing = form;
    try {
      form.requestSubmit(btn || undefined);
    } finally {
      releasing = null;
      if (btn) {
        btn.disabled = wasDisabled;
        if (busyValue !== null) btn.value = busyValue;
      }
    }
  }

  // A navigation started while a submit was held: drop every held
  // submit and hand the forms back, so a navigation that is then
  // cancelled (a download, a beforeunload prompt) leaves no form
  // spinning.
  function dropHeld() {
    pending.forEach(function (timer, form) {
      clearTimeout(timer);
      busyOff(form);
    });
    pending.clear();
    window.removeEventListener("beforeunload", dropHeld);
  }

  // Leaving the page — a link, Back — while a submit is held drops it:
  // the visitor has gone somewhere else, and a timer surviving into the
  // back/forward cache would otherwise send it on their return.
  window.addEventListener("pagehide", function () { dropHeld(); });

  // The back/forward cache restores a page's DOM exactly as it was left
  // — busy buttons still disabled, still wearing the busy label and the
  // spinner — so a visitor who navigates back finds a dead form. Hand
  // every busy form back.
  window.addEventListener("pageshow", function (e) {
    if (e.persisted) document.querySelectorAll("form[aria-busy]").forEach(busyOff);
  });
})();
