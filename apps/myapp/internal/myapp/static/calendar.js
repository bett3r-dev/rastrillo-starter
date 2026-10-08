/* calendar.js — the month grid the date fields open when you press
   their calendar button. A sibling of rastrillo.js, select.js and
   datetime.js, on exactly the same terms: first-party, dependency-free,
   inert by default, and app-owned from the moment rastrillo new writes
   it into static/.

   WHY IT IS ITS OWN FILE. datetime.js used to hand this job to
   native.showPicker(), which is one line and, in a form, close to
   useless: the panel it opens belongs to the browser, it cannot be
   styled to match the page, it does not exist at all for a time input
   on some engines, and it opens over the very field you were typing
   into. Replacing it takes a real widget — six weeks of days, a month
   that pages, a roving tabindex, a live preview of what the text
   beside it currently reads as — and datetime.js was already at the
   size where its own test says "split something out before it stops
   being readable". This is the split. Nothing here parses anything,
   and nothing in datetime.js draws a grid.

   THE SEAM. This file publishes one function on the page:

     window.rastrilloCalendar(cfg) -> a panel

   datetime.js reads it at enhance time and falls back to the native
   picker when it is absent, so a page that links one script and not
   the other still works — the same defensive posture both files take
   towards a missing attribute. cfg is documented on makePanel below.

   The grid is a real <table> with real column headers, because that is
   what a calendar is, and role="grid" over the top with a roving
   tabindex so exactly one day is in the tab order at a time. Arrow
   keys walk days and weeks, Page keys walk months, Shift+Page walks
   years, Home and End reach the ends of the week, and in a
   right-to-left page the horizontal arrows swap over, because "left"
   means "back" only in a page that runs the other way.

   It knows no month names and no weekday names by heart: it asks Intl
   for them in the page's own language, numbers its days with the
   locale's own digits, and starts its weeks on the day that locale
   starts weeks on. The three functions that decide all of it are pure
   and exported for Node at the bottom of the file, behind a guard a
   browser never takes; ui/datetime_node.mjs drives them with
   --calendar. */
(function (global) {
  "use strict";

  /* ── the arithmetic ──────────────────────────────────────────────
     Pure: no document, no state, no strings of its own. This is the
     half of a calendar that can be quietly WRONG — a grid that drops a
     day in a zone that moved its clocks that morning, a week that
     starts on the wrong day, headings that do not describe their own
     columns — so it is the half a test can reach without a browser. */

  // The day a week starts on here: 0 for Sunday through 6 for
  // Saturday, the numbering getDay uses. Asked of Intl where Intl will
  // answer — getWeekInfo is the standard spelling and weekInfo the
  // older property one, and both sit above this framework's browser
  // floor — and Monday where it will not.
  //
  // Monday is the ISO week's first day and most of the world's, and a
  // table of exceptions per country is precisely the thing these files
  // exist not to keep. It is also the cheapest possible thing to be
  // wrong about, because the column HEADINGS are derived from the same
  // number: a locale this browser has no week info for gets a correct
  // calendar that happens to start on Monday, never a calendar whose
  // headings lie about its columns.
  function firstDayOfWeek(locale) {
    var info = null, loc;
    try {
      loc = new Intl.Locale(locale || "en");
      info = typeof loc.getWeekInfo === "function" ? loc.getWeekInfo() : loc.weekInfo;
    } catch (e) {
      info = null;
    }
    // weekInfo counts Monday as 1 through Sunday as 7; getDay counts
    // Sunday as 0 through Saturday as 6. Seven modulo seven is zero,
    // which is exactly where Sunday belongs.
    if (info && typeof info.firstDay === "number" && info.firstDay >= 1 && info.firstDay <= 7) {
      return info.firstDay % 7;
    }
    return 1;
  }

  // The six weeks a calendar draws for one month: always 42 days,
  // beginning on this locale's own first day of the week.
  //
  // Counted in whole days through the Date constructor rather than by
  // adding milliseconds, because a day is not 86,400,000 milliseconds
  // long in a zone that moves its clocks: a grid built by arithmetic
  // on instants loses a day one Sunday in spring and repeats one in
  // autumn, in every zone that observes it. The constructor normalises
  // a day number out of range into the neighbouring month, which is
  // how the leading and trailing days arrive with no second branch.
  //
  // Six rows rather than as many as the month happens to need, so the
  // panel keeps one height all year. A grid that grows a row while you
  // are reaching for a date moves the date you were reaching for.
  function monthGrid(year, month, first) {
    var start = new Date(year, month, 1);
    var back = (start.getDay() - first + 7) % 7;
    var out = [], i;
    for (i = 0; i < 42; i++) out.push(new Date(year, month, 1 - back + i));
    return out;
  }

  // The seven column headings, in this locale's own words and in its
  // own order: a short form for the eye and a long one for a screen
  // reader, which is the pair an accessible calendar needs and neither
  // half of which is written down here.
  function weekdayNames(locale, first) {
    var out = [], i, day, probe, shortFmt = null, longFmt = null;
    try {
      shortFmt = new Intl.DateTimeFormat(locale, { weekday: "short", timeZone: "UTC" });
      longFmt = new Intl.DateTimeFormat(locale, { weekday: "long", timeZone: "UTC" });
    } catch (e) {
      shortFmt = null;
      longFmt = null;
    }
    for (i = 0; i < 7; i++) {
      day = (first + i) % 7;
      // 1 Feb 2026 is a Sunday, so the probes land index-aligned with
      // getDay — the same trick datetime.js builds its weekday table
      // on, and the reason both files can talk about "day 0" and mean
      // the same day.
      probe = new Date(Date.UTC(2026, 1, 1 + day));
      out.push({
        day: day,
        short: shortFmt ? shortFmt.format(probe) : String(day),
        long: longFmt ? longFmt.format(probe) : String(day)
      });
    }
    return out;
  }

  /* ── the panel ───────────────────────────────────────────────────
     Everything below is behind the document guard, so Node loads the
     arithmetic above and nothing else. */

  if (typeof document !== "undefined") {

    var CHEVRON = '<svg viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" ' +
      'stroke-linecap="round" stroke-linejoin="round" aria-hidden="true" focusable="false">' +
      '<path d="M15 5l-7 7 7 7"/></svg>';


    function pad(n) { return (n < 10 ? "0" : "") + n; }

    // A day, as the only key a calendar ever needs to compare by.
    // Timestamps are the wrong comparison here and wrong in a way that
    // shows up twice a year: two Dates an hour apart across a clock
    // change are the same square on the grid.
    function dayKey(d) {
      return d.getFullYear() + "-" + pad(d.getMonth() + 1) + "-" + pad(d.getDate());
    }

    function sameDay(a, b) { return !!a && !!b && dayKey(a) === dayKey(b); }

    function shift(d, days) {
      return new Date(d.getFullYear(), d.getMonth(), d.getDate() + days);
    }

    function shiftMonths(d, months) {
      // Clamped rather than rolled over: 31 January plus a month is 28
      // February, not 3 March. A Page key that lands you in the month
      // after the one you asked for has moved the cursor somewhere you
      // cannot see it.
      var y = d.getFullYear(), m = d.getMonth() + months, day = d.getDate();
      var last = new Date(y, m + 1, 0).getDate();
      return new Date(y, m, day > last ? last : day);
    }

    function fmt(locale, opts) {
      try {
        return new Intl.DateTimeFormat(locale, opts);
      } catch (e) {
        return null;
      }
    }

    // makePanel(cfg) builds one calendar and hands back the handful of
    // things its owner needs to drive it.
    //
    //   cfg.id        string — prefix for the ids this panel generates
    //   cfg.locale    BCP-47 tag, or undefined for the browser's own
    //   cfg.labels    { calendar, prev, next } — every visible or
    //                 spoken string, already translated. There are no
    //                 others: the month names, the weekday names and
    //                 the digits all come from Intl.
    //   cfg.value     () -> Date|null — the committed date, asked for
    //                 fresh on every open so the panel cannot show a
    //                 stale selection
    //   cfg.bounds    () -> { min: Date|null, max: Date|null }, also
    //                 asked fresh: a range's end is bounded by its
    //                 start, and the start moves
    //   cfg.onPick    (Date) -> void — a day was chosen
    //   cfg.onDismiss () -> void — Escape, or focus should go home
    //
    // Returns { el, open, close, isOpen, show, focusGrid }.
    function makePanel(cfg) {
      var locale = cfg.locale;
      var first = firstDayOfWeek(locale);
      var heads = weekdayNames(locale, first);
      var titleFmt = fmt(locale, { month: "long", year: "numeric" });
      var fullFmt = fmt(locale, { weekday: "long", day: "numeric", month: "long", year: "numeric" });
      var numFmt;
      try {
        numFmt = new Intl.NumberFormat(locale, { useGrouping: false });
      } catch (e) {
        numFmt = null;
      }

      var view = null;     // a Date inside the month on screen
      var cursor = null;   // the day the roving tabindex sits on
      var preview = null;  // what the text beside the panel currently reads as
      var cells = [];      // [{td, date}] in grid order

      var el = document.createElement("div");
      el.setAttribute("rst-cal", "");
      el.hidden = true;
      el.setAttribute("role", "dialog");
      el.setAttribute("aria-label", cfg.labels.calendar);

      var head = document.createElement("div");
      head.setAttribute("rst-cal-head", "");

      function navButton(label, which) {
        var b = document.createElement("button");
        b.type = "button";
        b.setAttribute("rst-cal-nav", which);
        b.setAttribute("aria-label", label);
        b.innerHTML = CHEVRON;
        return b;
      }

      var prevBtn = navButton(cfg.labels.prev, "prev");
      var nextBtn = navButton(cfg.labels.next, "next");

      var title = document.createElement("div");
      title.setAttribute("rst-cal-title", "");
      title.id = cfg.id + "-cal-title";
      // The month is announced as it changes, which is the only way a
      // screen-reader user paging through the year knows where they
      // are — the grid's own cells each name their full date, but
      // nothing else says "September 2026" out loud.
      title.setAttribute("aria-live", "polite");

      head.appendChild(prevBtn);
      head.appendChild(title);
      head.appendChild(nextBtn);

      var table = document.createElement("table");
      table.setAttribute("rst-cal-grid", "");
      table.setAttribute("role", "grid");
      table.setAttribute("aria-labelledby", title.id);

      var thead = document.createElement("thead");
      var headRow = document.createElement("tr");
      headRow.setAttribute("role", "row");
      for (var h = 0; h < heads.length; h++) {
        var th = document.createElement("th");
        th.setAttribute("scope", "col");
        th.setAttribute("role", "columnheader");
        // Two spellings, one cell: the short form for the eye, the
        // full name for a screen reader. An abbr attribute would have
        // been tidier and is read inconsistently, and a bare "Mo" read
        // aloud is not a weekday.
        var eye = document.createElement("span");
        eye.setAttribute("aria-hidden", "true");
        eye.textContent = heads[h].short;
        var said = document.createElement("span");
        said.className = "rst-sr-only";
        said.textContent = heads[h].long;
        th.appendChild(eye);
        th.appendChild(said);
        headRow.appendChild(th);
      }
      thead.appendChild(headRow);

      var tbody = document.createElement("tbody");
      table.appendChild(thead);
      table.appendChild(tbody);
      el.appendChild(head);
      el.appendChild(table);

      function outOfBounds(d) {
        var b = cfg.bounds ? cfg.bounds() : null;
        if (!b) return false;
        if (b.min && dayKey(d) < dayKey(b.min)) return true;
        if (b.max && dayKey(d) > dayKey(b.max)) return true;
        return false;
      }

      // A month is reachable if any day in it is. Asked of the first
      // and last day rather than of the 1st alone, because a min in
      // the middle of a month still leaves the rest of it pickable.
      function monthReachable(d) {
        var b = cfg.bounds ? cfg.bounds() : null;
        if (!b) return true;
        var lo = new Date(d.getFullYear(), d.getMonth(), 1);
        var hi = new Date(d.getFullYear(), d.getMonth() + 1, 0);
        if (b.min && dayKey(hi) < dayKey(b.min)) return false;
        if (b.max && dayKey(lo) > dayKey(b.max)) return false;
        return true;
      }

      function render() {
        var today = new Date();
        var chosen = cfg.value ? cfg.value() : null;
        var days = monthGrid(view.getFullYear(), view.getMonth(), first);
        var i, row, td, d, focusable = -1;

        title.textContent = titleFmt ? titleFmt.format(view)
          : view.getFullYear() + "-" + pad(view.getMonth() + 1);

        prevBtn.disabled = !monthReachable(shiftMonths(new Date(view.getFullYear(), view.getMonth(), 1), -1));
        nextBtn.disabled = !monthReachable(shiftMonths(new Date(view.getFullYear(), view.getMonth(), 1), 1));

        tbody.textContent = "";
        cells = [];
        for (i = 0; i < days.length; i++) {
          if (i % 7 === 0) {
            row = document.createElement("tr");
            row.setAttribute("role", "row");
            tbody.appendChild(row);
          }
          d = days[i];
          td = document.createElement("td");
          td.setAttribute("rst-cal-day", "");
          td.setAttribute("role", "gridcell");
          td.tabIndex = -1;
          td.textContent = numFmt ? numFmt.format(d.getDate()) : String(d.getDate());
          // The cell says its whole date, so a screen reader announces
          // "Saturday 5 September 2026" rather than "5" — which is all
          // the visible number can ever mean on its own.
          td.setAttribute("aria-label", fullFmt ? fullFmt.format(d) : dayKey(d));
          td.setAttribute("data-rst-day", dayKey(d));

          if (d.getMonth() !== view.getMonth()) td.classList.add("is-outside");
          if (sameDay(d, today)) {
            td.classList.add("is-today");
            td.setAttribute("aria-current", "date");
          }
          // aria-selected is the committed value and nothing else. The
          // preview below is a different claim — "this is what the
          // text currently reads as" — and saying both with the same
          // attribute would tell a screen reader a date had been
          // chosen every time somebody typed a letter.
          td.setAttribute("aria-selected", sameDay(d, chosen) ? "true" : "false");
          if (sameDay(d, chosen)) td.classList.add("is-selected");
          if (sameDay(d, preview)) td.classList.add("is-preview");
          if (outOfBounds(d)) {
            td.classList.add("is-disabled");
            td.setAttribute("aria-disabled", "true");
          }
          if (sameDay(d, cursor)) focusable = i;
          row.appendChild(td);
          cells.push({ td: td, date: d });
        }
        // Exactly one day in the tab order. If the cursor has walked
        // off this month — which it does the moment you page — the
        // first day of the month takes it, so Tab never lands on
        // nothing.
        if (focusable < 0) {
          for (i = 0; i < cells.length; i++) {
            if (cells[i].date.getMonth() === view.getMonth()) { focusable = i; break; }
          }
          if (focusable >= 0) cursor = cells[focusable].date;
        }
        if (focusable >= 0) cells[focusable].td.tabIndex = 0;
      }

      function cellFor(d) {
        var key = dayKey(d), i;
        for (i = 0; i < cells.length; i++) if (dayKey(cells[i].date) === key) return cells[i].td;
        return null;
      }

      // Move the cursor, redrawing the month first where the new day
      // is not on screen. focus says whether the keyboard is driving:
      // typing in the text field moves the cursor too, and stealing
      // focus out of the field mid-word would be unusable.
      function moveTo(d, focus) {
        cursor = d;
        if (d.getFullYear() !== view.getFullYear() || d.getMonth() !== view.getMonth()) {
          view = new Date(d.getFullYear(), d.getMonth(), 1);
        }
        render();
        if (focus) {
          var td = cellFor(d);
          if (td) td.focus();
        }
      }

      function pick(d) {
        if (outOfBounds(d)) return;
        if (cfg.onPick) cfg.onPick(new Date(d.getFullYear(), d.getMonth(), d.getDate()));
      }

      function dayAt(node) {
        while (node && node !== tbody) {
          if (node.getAttribute && node.getAttribute("data-rst-day")) return node;
          node = node.parentNode;
        }
        return null;
      }

      tbody.addEventListener("mousedown", function (e) {
        // Keep focus where it is: the field behind this panel is a
        // combobox, and a blur on the way to a click would commit the
        // half-typed text before the click ever landed.
        var td = dayAt(e.target);
        if (td) e.preventDefault();
      });

      tbody.addEventListener("click", function (e) {
        var td = dayAt(e.target), i;
        if (!td) return;
        for (i = 0; i < cells.length; i++) if (cells[i].td === td) return pick(cells[i].date);
      });

      // Which way is forward. In a page that runs right to left the
      // horizontal arrows swap, because "left" means "back" only in a
      // page that runs the other way.
      function rtl() {
        try {
          return getComputedStyle(el).direction === "rtl";
        } catch (e) {
          return false;
        }
      }

      table.addEventListener("keydown", function (e) {
        var step = rtl() ? -1 : 1, at = cursor || view;
        switch (e.key) {
          case "ArrowLeft": e.preventDefault(); return moveTo(shift(at, -step), true);
          case "ArrowRight": e.preventDefault(); return moveTo(shift(at, step), true);
          case "ArrowUp": e.preventDefault(); return moveTo(shift(at, -7), true);
          case "ArrowDown": e.preventDefault(); return moveTo(shift(at, 7), true);
          case "Home":
            e.preventDefault();
            return moveTo(shift(at, -((at.getDay() - first + 7) % 7)), true);
          case "End":
            e.preventDefault();
            return moveTo(shift(at, 6 - ((at.getDay() - first + 7) % 7)), true);
          case "PageUp":
            e.preventDefault();
            return moveTo(shiftMonths(at, e.shiftKey ? -12 : -1), true);
          case "PageDown":
            e.preventDefault();
            return moveTo(shiftMonths(at, e.shiftKey ? 12 : 1), true);
          case "Enter":
          case " ":
            e.preventDefault();
            return pick(at);
          case "Escape":
            e.preventDefault();
            if (cfg.onDismiss) cfg.onDismiss();
            return;
        }
      });

      prevBtn.addEventListener("mousedown", function (e) { e.preventDefault(); });
      nextBtn.addEventListener("mousedown", function (e) { e.preventDefault(); });
      prevBtn.addEventListener("click", function () {
        view = shiftMonths(new Date(view.getFullYear(), view.getMonth(), 1), -1);
        render();
      });
      nextBtn.addEventListener("click", function () {
        view = shiftMonths(new Date(view.getFullYear(), view.getMonth(), 1), 1);
        render();
      });

      return {
        el: el,

        // Open on a day: the one passed, else the committed value,
        // else today. Asked fresh every time, because the value can
        // have changed while the panel was shut.
        open: function (at) {
          var start = at || (cfg.value ? cfg.value() : null) || new Date();
          // Opening ON a day the text already named marks it as the
          // preview, because that is what it is: what the words say,
          // not what has been chosen. Opening on the committed value or
          // on today marks nothing — there is no reading to preview.
          preview = at ? new Date(at.getFullYear(), at.getMonth(), at.getDate()) : null;
          cursor = new Date(start.getFullYear(), start.getMonth(), start.getDate());
          view = new Date(start.getFullYear(), start.getMonth(), 1);
          render();
          el.hidden = false;
        },

        close: function () {
          el.hidden = true;
          preview = null;
        },

        isOpen: function () { return !el.hidden; },

        // The live preview: what the text beside the panel currently
        // reads as, marked on the grid and paged to without taking
        // focus. Null clears the mark and leaves the month where it is
        // — text that stopped parsing is not a reason to jump the
        // calendar somewhere else.
        show: function (d) {
          if (el.hidden) return;
          preview = d ? new Date(d.getFullYear(), d.getMonth(), d.getDate()) : null;
          if (preview) moveTo(preview, false);
          else render();
        },

        focusGrid: function () {
          if (el.hidden) return false;
          var td = cursor ? cellFor(cursor) : null;
          if (!td) return false;
          td.focus();
          return true;
        }
      };
    }

    global.rastrilloCalendar = makePanel;
  }

  // The pure half, for ui/datetime_node.mjs --calendar. A browser has
  // no `module` and never takes this branch.
  if (typeof module !== "undefined" && module && module.exports) {
    module.exports = {
      firstDayOfWeek: firstDayOfWeek,
      monthGrid: monthGrid,
      weekdayNames: weekdayNames
    };
  }
})(typeof globalThis !== "undefined" ? globalThis : this);
