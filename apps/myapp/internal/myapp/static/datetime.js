/* datetime.js — the natural-language date combobox. A sibling of
   rastrillo.js and select.js, on exactly the same terms: first-party,
   dependency-free, inert by default, and app-owned from the moment
   rastrillo new writes it into static/.

   Type "tomorrow", "next fri 9am", "25 Dec 6pm", "in 2 weeks" or
   "14:30" and the combobox resolves it, previews the reading, and
   writes it back to the native input on commit. The native input never
   leaves the DOM: it keeps its name and its wire value, so every POST
   is byte-identical to the un-enhanced form and the server keeps
   parsing it with the same code. With scripts off the field is an
   ordinary <input type="date">, and the browser's own picker still
   opens.

   Vocabulary:
     data-rst-date               on a date or datetime-local input: arm
                                 the combobox
     data-rst-time               the same, on a time input — the script
                                 reads a clock rather than a calendar
     data-rst-date-words         the parser vocabulary, one JSON object
                                 of |-separated spellings (see
                                 ui/funcs.go's dateWords)
     data-rst-date-set           the commit affordance's label
     data-rst-date-hint          guidance under the list, {example}
                                 substituted here with a date this
                                 locale's own formatter wrote
     data-rst-date-pick          label for the calendar button
     data-rst-date-pick-time     label for a TIME field's button, which
                                 opens a clock rather than a calendar
     data-rst-date-calendar      the calendar overlay's own name
     data-rst-date-prev-month    label for the overlay's back chevron
     data-rst-date-next-month    label for the overlay's forward one
     data-rst-date-results       live-region text, {n} substituted
     data-rst-date-result-one    live-region text for exactly one
     data-rst-date-quick-*       the seven quick-pick labels: today,
                                 tomorrow, next-week, plus-1h, plus-2h,
                                 end-of-day, next-day
     data-rst-range              on a WRAPPER around two armed inputs:
                                 the two pair up by DOM order (first is
                                 the start, second the end). The end
                                 gets quick picks relative to the
                                 start. A value of "session" also seeds
                                 an empty end to start + 1h when the
                                 start is committed.

   ui/partials/field-date.html, field-time.html, field-datetime.html
   and field-daterange.html emit all of it. Every string comes from the
   framework's base catalog (rastrillo.ui.date_*) and rides out on
   attributes, because this markup is built in the browser where the
   catalog is out of reach — so this file carries no English
   VOCABULARY: not one word the parser matches on is written here. It
   does carry English fallbacks for the eleven labels it puts on
   screen, exactly as select.js does, so the file still works standing
   alone in a page that forgot an attribute.

   The parser is a pure function of (text, vocabulary, locale tables,
   now). It knows no month names and no weekday names by heart: it asks
   Intl for them, in the page's own language, and folds the digits the
   same way, so a field enhanced in Hindi reads Devanagari digits and
   Hindi month names without a table anyone has to maintain. The
   vocabulary — "tomorrow", "next", "in", "ago" — is the half a machine
   cannot derive, and that half arrives translated on the attribute.

   The pure half is exported for Node at the bottom of the file, behind
   a guard a browser never takes; ui/datetime_node.mjs drives it
   against ui/testdata/datetime/*.json.

   Two things live NEXT to the strict grammar rather than inside it.
   parseAll offers a reading where the grammar refuses one, by finishing
   a half-typed word into the names this locale actually has: "5j" is
   not a date, and 5 January, 5 June and 5 July are three, nearest
   first. It never overrules the grammar — an exact reading wins
   outright — and every completion is shown with its date written out
   before anyone commits it.

   And the calendar itself is ui/calendar.js, a sibling file. This one
   reads and writes dates as TEXT; that one draws six weeks of them.
   The seam is window.rastrilloCalendar, looked up at enhance time, so
   an app that ships this file without that one still has a working
   field — the button falls back to the browser's own picker, the same
   way everything here falls back when an attribute is missing. */
(function () {
  "use strict";

  /* ── the parser ──────────────────────────────────────────────────
     Pure: no document, no state, no strings of its own. */

  var COMBINING = /[\u0300-\u036f]/g;

  // Scripts that separate words with spaces. A phrase may only match
  // between word boundaries in these, so "at" cannot be found inside
  // "atlas" — while an affixing script (Japanese 後, Chinese 后) is
  // deliberately left free to attach straight onto a quantity. Digits
  // are excluded on purpose: "9am" and "25dec" are one run to a
  // tokenizer and two words to a person.
  var LETTERISH = /[a-z\u00c0-\u024f\u0370-\u03ff\u0400-\u04ff\u0530-\u058f\u0590-\u05ff\u0600-\u06ff\u0900-\u097f\u0980-\u09ff\u0b80-\u0bff\u0e00-\u0e7f]/;
  // The punctuation that separates words, and not only the ASCII of
  // it: Arabic writes its comma \u060c and its semicolon \u061b, and CJK
  // writes \uff0c\u3001\u3002. Left out, those characters fall through to the
  // unknown-run branch and refuse a phrase that was punctuated
  // correctly in its own script.
  var BREAKS = /[\s,;\u060c\u061b\uff0c\u3001\u3002]/;

  function pad(n) { return (n < 10 ? "0" : "") + n; }

  function midnight(d) {
    return new Date(d.getFullYear(), d.getMonth(), d.getDate());
  }

  function shiftDays(d, n) {
    var x = midnight(d);
    x.setDate(x.getDate() + n);
    return x;
  }

  // Strictly the NEXT one: a weekday named on the day it falls means
  // the one a week out, never today. Naming today has a word of its
  // own in every catalog, so the ambiguity buys nothing.
  function comingWeekday(now, target) {
    var delta = (target - now.getDay() + 7) % 7;
    return shiftDays(now, delta === 0 ? 7 : delta);
  }

  function lastWeekday(now, target) {
    var delta = (now.getDay() - target + 7) % 7;
    return shiftDays(now, -(delta === 0 ? 7 : delta));
  }

  // A day-and-month with no year means the next time it comes round.
  function inferYear(now, mo, d) {
    var y = now.getFullYear();
    return new Date(y, mo, d) < midnight(now) ? y + 1 : y;
  }

  // Checked, never trusted: a 31st of February rolls silently forward
  // in the Date constructor, which is how a typo becomes a date nobody
  // meant. Built, read back, and refused if it moved.
  function makeDate(y, mo, d, h, mi) {
    // The carrier's wire format is four digits of year, so a year below
    // a thousand writes "500-12-25" — which every date input rejects by
    // silently emptying itself. A reading that clears the field is not
    // a reading, and the floor lives here rather than beside each
    // caller so no path can grow around it: the wire formats, the
    // numeric d/m/y, "25 dec 0500" and a bare year all pass through.
    if (y < 1000) return null;
    if (mo < 0 || mo > 11 || d < 1 || d > 31) return null;
    if (h < 0 || h > 23 || mi < 0 || mi > 59) return null;
    var dt = new Date(y, mo, d, h, mi, 0, 0);
    if (dt.getMonth() !== mo || dt.getDate() !== d) return null;
    return dt;
  }

  // The numbering systems a person may type in, probed against the
  // locale and unioned into the fold. The locale's default is not
  // enough on its own: this ICU resolves plain "ar" and plain "hi" to
  // latn, so those fields read "25" and refused ٢٥ and २५ — the digits
  // their keyboards make. Unioning DIGITS is safe in a way that unioning
  // words never is: a digit means the same number in every language, so
  // an English field that also takes ٢٥ has guessed nothing. Systems
  // that spell a number a word at a time are absent on purpose; they
  // cannot be read back a character at a time either.
  var NUMBERING = ["arab", "arabext", "beng", "deva"];

  // One system's ten digits, merged in. False where it adds nothing, or
  // cannot be folded at all: more than a character to a digit, a digit
  // written twice, or a fold contradicting one already agreed.
  function foldDigits(nf, map) {
    var i, parts, k, digit, add = {}, differs = false;
    for (i = 0; i <= 9; i++) {
      parts = nf.formatToParts(i);
      digit = "";
      for (k = 0; k < parts.length; k++) if (parts[k].type === "integer") digit = parts[k].value;
      if (digit.length !== 1) return false;
      if (add[digit] !== undefined) return false;
      if (map[digit] !== undefined && map[digit] !== String(i)) return false;
      if (digit !== String(i)) differs = true;
      add[digit] = String(i);
    }
    for (digit in add) if (Object.prototype.hasOwnProperty.call(add, digit)) map[digit] = add[digit];
    return differs;
  }

  // The ten digits this locale writes, mapped back to ASCII. ar-EG
  // counts in "١٢٣", which neither \d nor unary + accepts, and a
  // NumberFormat in that locale printing 0-9 IS the table. null where
  // there is nothing to fold, and null for a numbering system that
  // does not spell a number one digit at a time — it cannot be read
  // back one digit at a time either.
  function digitFolder(locale) {
    var map = {}, differs = false, base, root, i;
    try {
      base = new Intl.NumberFormat(locale, { useGrouping: false });
    } catch (e) {
      return null;
    }
    if (foldDigits(base, map)) differs = true;
    // The tag the probes hang off, with any -u- extension the resolver
    // already added trimmed off: a second one is an ill-formed tag.
    try {
      root = base.resolvedOptions().locale.split("-u-")[0];
    } catch (e) {
      root = "";
    }
    for (i = 0; root && i < NUMBERING.length; i++) {
      try {
        if (foldDigits(new Intl.NumberFormat(root + "-u-nu-" + NUMBERING[i], { useGrouping: false }), map)) differs = true;
      } catch (e) {
        /* a numbering system this engine does not know folds nothing */
      }
    }
    if (!differs) return null;
    return function (s) {
      var out = "", j;
      for (j = 0; j < s.length; j++) out += map[s.charAt(j)] !== undefined ? map[s.charAt(j)] : s.charAt(j);
      return out;
    };
  }

  // One normalizer, used on both sides — on the vocabulary and the
  // Intl names as the tables are built, and on the typed text as it is
  // read — so a person who leaves the accents off still meets the
  // words the catalog spells with them.
  function makeFold(digits) {
    return function (s) {
      var out = String(s === null || s === undefined ? "" : s);
      if (out.normalize) out = out.normalize("NFD").replace(COMBINING, "");
      out = out.toLowerCase();
      if (digits) out = digits(out);
      return out.replace(/\s+/g, " ").trim();
    };
  }

  // What this locale calls each month (or weekday), long and short,
  // index-aligned with the probe dates — which is what turns a matched
  // word back into a number. Asked of Intl rather than tabulated,
  // because a table per language is precisely the thing this file
  // exists not to keep.
  function nameTable(locale, type, dates, fold) {
    var widths = ["long", "short"], out = [], i, c, w, opts, fmt, parts, k, value;
    // Two shapes of every month name, because a month is not one word
    // in every language: a Russian calendar header says декабрь and a
    // Russian typing a date writes 25 декабря, and Intl only hands over
    // that second form when a DAY is in the format. Asking both ways
    // and keeping both answers is how a DERIVED table covers a case the
    // grammar decides — no genitive is written down here, and Ukrainian
    // and Greek are fixed by the same two lines.
    var contexts = type === "month" ? [null, "numeric"] : [null];
    for (i = 0; i < dates.length; i++) out.push([]);
    for (c = 0; c < contexts.length; c++)
    for (w = 0; w < widths.length; w++) {
      opts = { timeZone: "UTC" };
      opts[type] = widths[w];
      if (contexts[c]) opts.day = contexts[c];
      try {
        fmt = new Intl.DateTimeFormat(locale, opts);
      } catch (e) {
        continue;
      }
      for (i = 0; i < dates.length; i++) {
        parts = fmt.formatToParts(dates[i]);
        value = "";
        for (k = 0; k < parts.length; k++) if (parts[k].type === type) value = parts[k].value;
        value = fold(value);
        if (!value) continue;
        // A NAME has to be a word. Japanese and Chinese write their
        // months as numbers, and Intl reports that as
        // {month:"8"}+{literal:"\u6708"} — so the "name" of August comes
        // back as the bare digit 8, and a table holding it turns every
        // typed number into a month. It did: ja "2025" tokenized as
        // month 2 plus 025 and committed 25 February 2027 without a
        // word of warning, which is the one thing this parser promises
        // never to do. Anything that is only digits and separators is
        // refused here rather than trusted; a calendar whose months are
        // numbers is one this grammar cannot read, and saying so is the
        // honest answer.
        if (!/[^\d\s.,\/\-]/.test(value)) continue;
        if (out[i].indexOf(value) === -1) out[i].push(value);
        // An abbreviation's full stop is dropped as often as it is
        // typed ("16 Aug." retyped as "16 Aug"), so both spellings are
        // in the table rather than tolerated by one regex somewhere.
        if (value.charAt(value.length - 1) === "." && value.length > 1) {
          value = value.slice(0, -1);
          if (out[i].indexOf(value) === -1) out[i].push(value);
        }
      }
    }
    return out;
  }

  // The options the field's own display formats with. Up here in the
  // pure half rather than beside the page code that uses it, because
  // the parser has to read that display back: what this field writes,
  // it must be able to re-read, and a reading that cannot is how an
  // in-place edit turns into a refusal.
  function displayOptions(kind) {
    var opts = kind === "time"
      ? { hour: "numeric", minute: "2-digit" }
      : { weekday: "short", day: "numeric", month: "short", year: "numeric" };
    if (kind === "datetime") {
      opts.hour = "numeric";
      opts.minute = "2-digit";
    }
    return opts;
  }

  // What Intl writes that nobody typed: the literals it threads between
  // the numbers (ja 年月日, ru's "г.", pt's "de", the brackets round a
  // Japanese weekday) and the dayPeriod names it chooses, which are not
  // always the ones a catalog spells — Intl writes yue's afternoon as
  // 下晝 where the catalog says 下午. Derived from the same options the
  // display uses, plus the day-and-month shapes, which is the form a
  // person types ("25 de marzo"). Literals come back ignorable and
  // dayPeriods come back as am/pm, so the field can read its own
  // writing without a per-language table anybody maintains.
  function displayTokens(locale, fold) {
    var probes = [displayOptions("date"), displayOptions("datetime"), displayOptions("time"),
      { day: "numeric", month: "long" }, { day: "numeric", month: "short" }];
    var when = [new Date(Date.UTC(2026, 11, 25, 15, 30)), new Date(Date.UTC(2026, 2, 3, 9, 5))];
    var out = { literal: {}, am: {}, pm: {} }, p, i, fmt, parts, k, v, c, run;
    function keep(bag, s) { s = fold(s); if (s) bag[s] = true; }
    for (p = 0; p < probes.length; p++) {
      probes[p].timeZone = "UTC";
      try {
        fmt = new Intl.DateTimeFormat(locale, probes[p]);
      } catch (e) {
        continue;
      }
      for (i = 0; i < when.length; i++) {
        parts = fmt.formatToParts(when[i]);
        for (k = 0; k < parts.length; k++) {
          v = parts[k].value;
          if (parts[k].type === "dayPeriod") {
            keep(when[i].getUTCHours() < 12 ? out.am : out.pm, v);
            continue;
          }
          if (parts[k].type !== "literal") continue;
          // One literal is not always one token: ja hands back "日(" in
          // a single part. Split into letter runs and single marks so
          // the day counter stays a day counter and the bracket becomes
          // its own ignorable thing.
          for (c = 0, run = ""; c <= v.length; c++) {
            if (c < v.length && letterish(v.charAt(c))) { run += v.charAt(c); continue; }
            keep(out.literal, run);
            run = "";
            if (c < v.length && !BREAKS.test(v.charAt(c))) keep(out.literal, v.charAt(c));
          }
        }
      }
    }
    return out;
  }

  var TABLES = {};

  // Everything derived from a locale, built once and cached: the fold,
  // the month and weekday names, the wall-clock formatters the field
  // shows its value with.
  function tables(locale) {
    var key = locale || "";
    if (TABLES[key]) return TABLES[key];
    var loc = locale || undefined;
    var fold = makeFold(digitFolder(loc));
    var monthDates = [], weekDates = [], i;
    for (i = 0; i < 12; i++) monthDates.push(new Date(Date.UTC(2026, i, 15)));
    // 1 Feb 2026 is a Sunday, so the seven probes land index-aligned
    // with Date.prototype.getDay.
    for (i = 0; i < 7; i++) weekDates.push(new Date(Date.UTC(2026, 1, 1 + i)));
    var t = {
      locale: loc,
      fold: fold,
      months: nameTable(loc, "month", monthDates, fold),
      weekdays: nameTable(loc, "weekday", weekDates, fold),
      display: displayTokens(loc, fold)
    };
    // Does this locale call its months anything at all? Japanese does
    // not — it numbers them, and nameTable will not hold a bare digit
    // as a name — so the table is empty and the grammar reads 12月25日
    // another way. Derived: no list of numbering calendars kept here.
    t.namedMonths = false;
    for (i = 0; i < t.months.length; i++) if (t.months[i].length) t.namedMonths = true;
    TABLES[key] = t;
    return t;
  }

  // The vocabulary and the Intl names as one list, longest phrase
  // first — so a plain scan cannot let "m" win over "may", and a
  // multi-word entry ("hôm nay", "منتصف الليل", "seo chugainn") is
  // matched whole. Splitting is on "|" only: "meio-dia" is one entry
  // and stays one.
  function index(vocab, tbl) {
    var list = [], name, alts, i, m, k;
    for (name in vocab) {
      if (!Object.prototype.hasOwnProperty.call(vocab, name)) continue;
      alts = String(vocab[name]).split("|");
      for (i = 0; i < alts.length; i++) {
        var text = tbl.fold(alts[i]);
        if (text) list.push({ text: text, role: "word", kind: name, at: -1 });
      }
    }
    // The display's own vocabulary, on the same list: a literal is a
    // word the reading may skip, and a dayPeriod is am or pm however
    // Intl chose to spell it. Both are additive — a spelling already
    // carrying a kind keeps it and gains this one, because matchAt
    // merges every entry of the same length.
    var seen = ["literal", "am", "pm"], kind;
    for (i = 0; i < seen.length; i++) {
      kind = seen[i] === "literal" ? "ignore" : seen[i];
      for (name in tbl.display[seen[i]])
        if (Object.prototype.hasOwnProperty.call(tbl.display[seen[i]], name))
          list.push({ text: name, role: "word", kind: kind, at: -1 });
    }
    for (m = 0; m < tbl.months.length; m++)
      for (k = 0; k < tbl.months[m].length; k++)
        list.push({ text: tbl.months[m][k], role: "month", kind: "", at: m });
    for (m = 0; m < tbl.weekdays.length; m++)
      for (k = 0; k < tbl.weekdays[m].length; k++)
        list.push({ text: tbl.weekdays[m][k], role: "weekday", kind: "", at: m });
    list.sort(function (a, b) { return b.text.length - a.text.length; });
    return list;
  }

  // The index for this table, built once and kept on it. Cached
  // against the vocabulary itself, not just the locale: a page could
  // in principle carry two, and a stale index would parse the second
  // field with the first field's words.
  function indexFor(vocab, tbl) {
    var key = "", name;
    for (name in vocab) if (Object.prototype.hasOwnProperty.call(vocab, name)) key += name + "=" + vocab[name] + "\u0000";
    if (tbl.indexKey !== key) {
      tbl.index = index(vocab, tbl);
      tbl.indexKey = key;
    }
    return tbl.index;
  }

  // A separator is never a letter, whatever block it lives in. The
  // Arabic comma sits at \u060c, inside the same range as the letters
  // either side of it, so a class drawn by range alone reads
  // "\u063a\u062f\u0627\u060c 6" as one unbroken word and refuses a phrase that was
  // punctuated correctly.
  function letterish(c) {
    return !!c && !BREAKS.test(c) && LETTERISH.test(c);
  }

  function boundaryOK(s, from, to) {
    if (from > 0 && letterish(s.charAt(from - 1)) && letterish(s.charAt(from))) return false;
    if (to < s.length && letterish(s.charAt(to)) && letterish(s.charAt(to - 1))) return false;
    return true;
  }

  // The longest phrase starting here, as one token. Every entry of that
  // same length is merged in, because one spelling can wear two hats:
  // Vietnamese "sau" is both "next" and "in", and which one it means is
  // a question the grammar answers, not the scanner.
  function matchAt(s, pos, idx) {
    var best = -1, tok = null, i, e;
    for (i = 0; i < idx.length; i++) {
      e = idx[i];
      if (best >= 0 && e.text.length < best) break;
      if (s.slice(pos, pos + e.text.length) !== e.text) continue;
      if (!boundaryOK(s, pos, pos + e.text.length)) continue;
      if (best < 0) {
        best = e.text.length;
        tok = { t: "word", kinds: {}, month: -1, weekday: -1 };
      }
      if (e.role === "word") tok.kinds[e.kind] = true;
      else if (e.role === "month") tok.month = e.at;
      else tok.weekday = e.at;
    }
    return tok ? { tok: tok, len: best } : null;
  }

  function scan(s, idx) {
    var toks = [], pos = 0, n = s.length, m, hit, start, glued = false;
    // Whether nothing at all stood between this token and the one
    // before it. A counter glued to its number belongs to that number
    // (12月, 25日); the same word with a space in front of it is a noun
    // being counted ("3 months"). A fact about the typing, not about
    // any language, which is why it can be recorded without knowing
    // which language was typed.
    //
    // Where the token was read is recorded alongside it, because
    // completion below rewrites exactly one token and hands the
    // sentence back to the grammar unchanged around it. A scanner that
    // forgets where it read a word cannot offer to finish it.
    function push(tok, from, to) {
      tok.glued = glued;
      tok.from = from;
      tok.to = to;
      toks.push(tok);
      glued = true;
    }
    while (pos < n) {
      if (BREAKS.test(s.charAt(pos))) { pos++; glued = false; continue; }
      m = /^(\d{1,2}):(\d{2})/.exec(s.slice(pos));
      if (m) {
        push({ t: "clock", h: +m[1], mi: +m[2] }, pos, pos + m[0].length);
        pos += m[0].length;
        continue;
      }
      hit = matchAt(s, pos, idx);
      if (hit) { push(hit.tok, pos, pos + hit.len); pos += hit.len; continue; }
      m = /^\d+/.exec(s.slice(pos));
      if (m) {
        push({ t: "num", n: +m[0], digits: m[0].length }, pos, pos + m[0].length);
        pos += m[0].length;
        continue;
      }
      start = pos;
      while (pos < n && !BREAKS.test(s.charAt(pos)) && !/\d/.test(s.charAt(pos))) {
        if (pos > start && matchAt(s, pos, idx)) break;
        pos++;
      }
      if (pos === start) pos++;
      push({ t: "other", raw: s.slice(start, pos) }, start, pos);
    }
    return toks;
  }

  // parse(text, vocab, tbl, now, ctx) -> {date, hasDate, hasTime} or
  // null. Never a guess: text this grammar does not understand comes
  // back null, so nothing is armed and nothing is committed.
  //
  // ctx.prev is the value this field is already showing; ctx.base is
  // the day a wired range's START is on. The two are not
  // interchangeable. A phrase naming no date at all ("5pm") lands on
  // the START's day — that is what makes a time-only end stay in the
  // same session. A phrase naming PART of a date ("2030") keeps the day
  // THIS field is showing, because the part it left out is the part it
  // means to keep.
  function parse(text, vocab, tbl, now, ctx) {
    ctx = ctx || {};
    var raw = tbl.fold(text);
    if (!raw) return null;
    var idx = indexFor(vocab, tbl);

    var prev = ctx.prev || null;
    var base = ctx.base || null;
    var anchor = midnight(base || prev || now);
    var own = midnight(prev || base || now);
    var clockH = prev ? prev.getHours() : 9;
    var clockM = prev ? prev.getMinutes() : 0;

    var m, d;

    // The wire formats, read straight back: what the native carrier
    // holds, and what a person pastes out of one form into another.
    m = /^(\d{4})-(\d{1,2})-(\d{1,2})(?:[t ](\d{1,2}):(\d{2})(?::\d{2})?)?$/.exec(raw);
    if (m) {
      d = makeDate(+m[1], +m[2] - 1, +m[3],
        m[4] === undefined ? clockH : +m[4],
        m[4] === undefined ? clockM : +m[5]);
      return d ? { date: d, hasDate: true, hasTime: m[4] !== undefined } : null;
    }
    // Year first, with the separators a keyboard reaches for rather
    // than the wire's hyphen. Nothing else in any calendar puts four
    // digits in front of a date, so 2026/12/25 and 2026.12.25 need no
    // day-or-month guess: it is how ja, zh and yue write one down.
    m = /^(\d{4})[\/.](\d{1,2})[\/.](\d{1,2})$/.exec(raw);
    if (m) {
      d = makeDate(+m[1], +m[2] - 1, +m[3], clockH, clockM);
      return d ? { date: d, hasDate: true, hasTime: false } : null;
    }
    // Numeric d/m[/y], day first — and swapped where only the other
    // reading can be true, so a pasted 12/25/2027 is understood rather
    // than refused.
    m = /^(\d{1,2})[\/.\-](\d{1,2})(?:[\/.\-](\d{2}|\d{4}))?$/.exec(raw);
    if (m) {
      var day = +m[1], mon = +m[2] - 1, swap;
      if (mon > 11 && day <= 12) { swap = day; day = +m[2]; mon = swap - 1; }
      var year = m[3] === undefined ? inferYear(now, mon, day)
        : (+m[3] < 100 ? 2000 + +m[3] : +m[3]);
      d = makeDate(year, mon, day, clockH, clockM);
      return d ? { date: d, hasDate: true, hasTime: false } : null;
    }

    var toks = scan(raw, idx), i;
    if (!toks.length) return null;
    for (i = 0; i < toks.length; i++) if (toks[i].t === "other") return null;

    var used = [];
    function find(kind) {
      for (var k = 0; k < toks.length; k++)
        if (!used[k] && toks[k].t === "word" && toks[k].kinds[kind]) return k;
      return -1;
    }
    function findRole(role) {
      for (var k = 0; k < toks.length; k++) {
        if (used[k] || toks[k].t !== "word" || toks[k][role] < 0) continue;
        // A weekday name being counted is not a weekday: nobody writes
        // a date by putting a number in front of one. A MONTH name is
        // the opposite case and is left alone — "25dec" is how people
        // type one.
        if (role === "weekday" && k > 0 && toks[k].glued && isCounter(k, k - 1)) continue;
        return k;
      }
      return -1;
    }
    function findKind(t) {
      for (var k = 0; k < toks.length; k++) if (!used[k] && toks[k].t === t) return k;
      return -1;
    }
    function after(k) {
      return k + 1 < toks.length && !used[k + 1] ? k + 1 : -1;
    }
    function before(k) {
      return k > 0 && !used[k - 1] ? k - 1 : -1;
    }
    function unit() {
      var names = ["minute", "hour", "day", "week", "month"], k, at;
      for (k = 0; k < names.length; k++) {
        at = find(names[k]);
        if (at >= 0) return { name: names[k], at: at };
      }
      return null;
    }
    // Is the name at k being counted rather than named? A number in
    // front of it says so, and so does a next/last particle written
    // straight onto a word that is a unit as well as a name: 来月 is
    // next MONTH and 翌日 is the next DAY, and both asked for a weekday
    // until this line, because 月 and 日 are also how Japanese
    // abbreviates Monday and Sunday.
    function isCounter(k, p) {
      if (toks[p].t === "num") return true;
      if (toks[p].t !== "word") return false;
      if (!toks[p].kinds.next && !toks[p].kinds.last) return false;
      return !!(toks[k].kinds.day || toks[k].kinds.week || toks[k].kinds.month);
    }
    function isWord(k, kind) {
      return k >= 0 && toks[k].t === "word" && !!toks[k].kinds[kind];
    }
    // The counter glued to a number already claimed — the 日 of 25日,
    // the 号 of 25号. It belongs to that number, and leaving it behind
    // failed the leftover check on a date written correctly. Glued is
    // the whole test: "3 days" has a space and stays a duration.
    function claimCounter(at, kind) {
      var nx = at >= 0 ? after(at) : -1;
      if (nx < 0 || toks[nx].t !== "word" || !toks[nx].glued) return false;
      if (!toks[nx].kinds[kind]) return false;
      used[nx] = true;
      return true;
    }
    // A number glued to the month counter where months have no names:
    // the 12 of 12月25日. Gated on the empty table so no language that
    // CALLS its months something reaches here — without it, English
    // "3months" would read as March.
    function counterMonth() {
      if (tbl.namedMonths) return -1;
      for (var k = 0; k < toks.length; k++) {
        if (used[k] || toks[k].t !== "num" || toks[k].digits > 2) continue;
        var nx = after(k);
        if (nx >= 0 && toks[nx].t === "word" && toks[nx].glued && toks[nx].kinds.month) return k;
      }
      return -1;
    }

    var date = null, hasDate = false, hasTime = false, h = 0, mi = 0;

    // A quantity is what tells "in"/"ago" apart from "next"/"last"
    // where one spelling carries both — Vietnamese "sau" is "next"
    // with nothing counted and "in" with a number beside it.
    var quantified = findKind("num") >= 0;
    var into = find("in"), back = find("ago");
    // Both directions at once is not a phrase, it is a contradiction:
    // "in 2 weeks ago" and "через 2 недели назад" name no instant, and
    // the sign was being picked by whichever branch ran second — ago
    // won, silently, and the reading looked confident. Two DISTINCT
    // tokens is the test, because one spelling is allowed to carry
    // both kinds (Vietnamese "sau" does) and a single word wearing two
    // hats is still one word.
    if (into >= 0 && back >= 0 && into !== back) return null;
    if (quantified && (into >= 0 || back >= 0)) {
      var u = unit();
      if (u) {
        var qi = findKind("num");
        var qty = toks[qi].n, sign = back >= 0 ? -1 : 1;
        used[qi] = true;
        used[u.at] = true;
        if (into >= 0) used[into] = true;
        if (back >= 0) used[back] = true;
        if (u.name === "minute" || u.name === "hour") {
          // An offset counted in clock units moves the clock, so it
          // names a time as well as a day.
          date = new Date(now.getTime() + sign * qty * (u.name === "hour" ? 3600000 : 60000));
          hasDate = true;
          hasTime = true;
          h = date.getHours();
          mi = date.getMinutes();
        } else {
          date = midnight(now);
          if (u.name === "day") date.setDate(date.getDate() + sign * qty);
          else if (u.name === "week") date.setDate(date.getDate() + sign * qty * 7);
          else date.setMonth(date.getMonth() + sign * qty);
          hasDate = true;
        }
      }
    }

    // The clock. Claimed before the calendar so "25 dec 6pm" cannot
    // read its 6 as a second day number.
    if (!hasTime) {
      var noon = find("noon"), mid = find("midnight");
      if (noon >= 0) { h = 12; mi = 0; hasTime = true; used[noon] = true; }
      else if (mid >= 0) { h = 0; mi = 0; hasTime = true; used[mid] = true; }
      else {
        var ci = findKind("clock");
        if (ci >= 0) {
          h = toks[ci].h;
          mi = toks[ci].mi;
          used[ci] = true;
          hasTime = true;
        } else {
          for (i = 0; i < toks.length && !hasTime; i++) {
            if (used[i] || toks[i].t !== "num") continue;
            var nx = after(i), pv = before(i);
            if (isWord(nx, "am") || isWord(nx, "pm") || isWord(pv, "am") || isWord(pv, "pm")) {
              h = toks[i].n;
              mi = 0;
              used[i] = true;
              hasTime = true;
              // A half-of-the-day marker and a clock marker can both
              // sit on the same hour: \u5348\u5f8c6\u6642 and \u4e0b\u53486\u70b9 are "6 in the
              // afternoon" with the hour word still attached. Claiming
              // the number and leaving the \u6642 behind failed the
              // leftover check and refused a perfectly ordinary phrase.
              if (isWord(nx, "hour")) {
                used[nx] = true;
                var mnx = after(nx);
                if (mnx >= 0 && toks[mnx].t === "num" && (after(mnx) < 0 || isWord(after(mnx), "minute"))) {
                  mi = toks[mnx].n;
                  used[mnx] = true;
                  if (after(mnx) >= 0) used[after(mnx)] = true;
                }
              }
            } else if (isWord(nx, "hour")) {
              h = toks[i].n;
              mi = 0;
              used[i] = true;
              used[nx] = true;
              hasTime = true;
              var mn = after(nx);
              if (mn >= 0 && toks[mn].t === "num" && (after(mn) < 0 || isWord(after(mn), "minute"))) {
                mi = toks[mn].n;
                used[mn] = true;
                if (after(mn) >= 0) used[after(mn)] = true;
              }
            } else if (isWord(pv, "at") || isWord(nx, "at")) {
              // The particle sits on either side of the hour: English
              // and Spanish put it in front ("at 6", "a las 6") and
              // Hindi, Bengali and Japanese put it behind (6 बजे, ৬ টায়,
              // 6時に). Reading only the front one refused half the
              // world's ordinary way of naming a time.
              h = toks[i].n;
              mi = 0;
              used[i] = true;
              used[isWord(pv, "at") ? pv : nx] = true;
              hasTime = true;
            }
          }
        }
        if (hasTime) {
          var pm = find("pm"), am = find("am");
          // A half-of-the-day marker says the clock beside it is a
          // 12-hour one, and a 12-hour clock has twelve hours on it.
          // The fold below is modular, so without this line every
          // impossible hour came back as a plausible one: "25pm" was
          // 13:00 and "99am" was 03:00 — a guess dressed as a reading,
          // and the leftover check could not see it because every token
          // had been claimed.
          //
          // Zero is one of the twelve. Japanese and Chinese write
          // midnight and noon as 午前0時 and 午後0時, 上午0点 and 下午0点 —
          // ordinary usage, not a typo — and the modular fold already
          // says the right thing about them: 0 with a morning marker is
          // midnight, 0 with an afternoon one is noon, exactly as 12
          // reads the other way round. That also lets English "0am"
          // through, which is coherent (it IS midnight) and much
          // simpler than a rule that would have to know which
          // languages say it.
          if (pm >= 0 || am >= 0) {
            if (h < 0 || h > 12) return null;
            if (pm >= 0) { used[pm] = true; h = (h % 12) + 12; }
            else { used[am] = true; h = h % 12; }
          }
        }
      }
      if (hasTime && (h > 23 || mi > 59)) return null;
    }

    // The calendar.
    if (!hasDate) {
      var tod = find("today"), tom = find("tomorrow"), yes = find("yesterday");
      if (tom >= 0) { date = shiftDays(now, 1); used[tom] = true; hasDate = true; }
      else if (yes >= 0) { date = shiftDays(now, -1); used[yes] = true; hasDate = true; }
      else if (tod >= 0) { date = midnight(now); used[tod] = true; hasDate = true; }
    }
    if (!hasDate) {
      var next = find("next"), last = find("last"), dup;
      var cm = counterMonth();
      var wi = findRole("weekday");
      var mo = findRole("month");
      if (wi >= 0 && mo >= 0) {
        if (wi === mo) {
          // ONE spelling wearing both hats: Spanish "mar" is March and
          // it is Tuesday, and the scanner merges same-length matches
          // rather than picking for you. Twice in a row is this field's
          // own writing — "mar, 3 mar 2026" is Tuesday and then March —
          // so a second month-naming token settles it and the first is
          // the decoration. Otherwise a day number beside it settles it
          // ("25 mar" is a date and nothing else), and with no number
          // to count, a weekday is what a person means.
          for (dup = wi + 1; dup < toks.length; dup++)
            if (!used[dup] && toks[dup].t === "word" && toks[dup].month >= 0) { mo = dup; break; }
          if (mo !== wi) { used[wi] = true; wi = -1; }
          else if (findKind("num") >= 0) wi = -1;
          else mo = -1;
        } else {
          // A weekday sitting beside a month and a day is decoration,
          // not the date: it is how this field writes its own value
          // back ("Fri, 28 Aug 2026"), and the only way to retype a
          // year in place is for the weekday beside it to be consumed
          // rather than obeyed. Obeying it would move the date to the
          // coming Friday and silently throw the typed year away.
          used[wi] = true;
          wi = -1;
        }
      } else if (wi >= 0 && cm >= 0) {
        // Same rule where the months are numbered rather than named:
        // 2026年12月25日(金) and 2026年12月25日周五 are a full date with
        // the weekday written beside it, and the weekday is the part
        // that follows from the rest.
        used[wi] = true;
        wi = -1;
      }
      if (wi >= 0) {
        used[wi] = true;
        if (last >= 0) { used[last] = true; date = lastWeekday(now, toks[wi].weekday); }
        else {
          if (next >= 0) used[next] = true;
          date = comingWeekday(now, toks[wi].weekday);
        }
        hasDate = true;
      } else {
        if (mo >= 0) {
          used[mo] = true;
          var dnum = -1, ynum = -1, dat = -1, k;
          for (k = 0; k < toks.length; k++) {
            if (used[k] || toks[k].t !== "num") continue;
            if (toks[k].digits === 4) {
              if (ynum >= 0) break;
              ynum = toks[k].n;
              used[k] = true;
            } else if (dnum < 0) {
              dnum = toks[k].n;
              dat = k;
              used[k] = true;
            } else if (ynum < 0) {
              ynum = toks[k].n < 100 ? 2000 + toks[k].n : toks[k].n;
              used[k] = true;
            } else break;
          }
          if (dnum < 0) return null;
          claimCounter(dat, "day");
          date = makeDate(ynum >= 0 ? ynum : inferYear(now, toks[mo].month, dnum),
            toks[mo].month, dnum, 0, 0);
          if (!date) return null;
          hasDate = true;
        } else if (cm >= 0) {
          // A calendar that numbers its months writes the number with
          // its counter, and the counter is doing the work a name does
          // everywhere else. The day beside it is written the same way,
          // with or without its own counter.
          var cmon = toks[cm].n - 1, cday = -1, cyear = -1, ck;
          used[cm] = true;
          used[cm + 1] = true;
          ck = after(cm + 1);
          if (ck >= 0 && toks[ck].t === "num" && toks[ck].digits <= 2) {
            cday = toks[ck].n;
            used[ck] = true;
            claimCounter(ck, "day");
          }
          if (cday < 0) return null;
          // The year goes in front here (2026年12月25日), so it is
          // whatever four digits are still unspoken for. Without this
          // the field could write a full date it could not read back.
          for (ck = 0; ck < toks.length && cyear < 0; ck++) {
            if (used[ck] || toks[ck].t !== "num" || toks[ck].digits !== 4) continue;
            cyear = toks[ck].n;
            used[ck] = true;
          }
          date = makeDate(cyear >= 0 ? cyear : inferYear(now, cmon, cday), cmon, cday, 0, 0);
          if (!date) return null;
          hasDate = true;
        } else if (next >= 0 || last >= 0) {
          var u2 = unit();
          if (u2 && (u2.name === "day" || u2.name === "week" || u2.name === "month")) {
            used[u2.at] = true;
            var dir = last >= 0 ? -1 : 1;
            used[last >= 0 ? last : next] = true;
            date = midnight(now);
            if (u2.name === "day") date.setDate(date.getDate() + dir);
            else if (u2.name === "week") date.setDate(date.getDate() + 7 * dir);
            else date.setMonth(date.getMonth() + dir);
            hasDate = true;
          }
        }
      }
    }
    if (!hasDate) {
      // Four digits on their own move the year and nothing else — the
      // same bargain a lone time strikes with the date. Focus selects
      // the whole value, so "2030" is not a fragment someone left
      // behind: it is the entire gesture of changing a year.
      var only = -1, count = 0;
      for (i = 0; i < toks.length; i++) {
        if (used[i] || toks[i].t !== "num") continue;
        count++;
        if (toks[i].digits === 4) only = i;
      }
      // Four digits, and a year makeDate's floor will accept — checked
      // here too so a refused year never claims its token and never
      // reads as a date at all.
      if (count === 1 && only >= 0 && toks[only].n >= 1000) {
        used[only] = true;
        date = new Date(own.getFullYear(), own.getMonth(), own.getDate());
        date.setFullYear(toks[only].n);
        hasDate = true;
      }
    }

    // Anything left over is a word this reading did not account for,
    // and a reading that ignores half the sentence is a guess. Two
    // exceptions: the "at" particle, which is optional everywhere it
    // appears and required nowhere; and a literal the display itself
    // writes, which nobody typed on purpose and which carries no
    // meaning to drop.
    for (i = 0; i < toks.length; i++) {
      if (used[i]) continue;
      if (toks[i].t !== "word") return null;
      if (toks[i].month >= 0 || toks[i].weekday >= 0) return null;
      for (var kind in toks[i].kinds) if (kind !== "at" && kind !== "ignore") return null;
    }
    if (!hasDate && !hasTime) return null;

    var on = hasDate ? date : anchor;
    var out = makeDate(on.getFullYear(), on.getMonth(), on.getDate(),
      hasTime ? h : clockH, hasTime ? mi : clockM);
    return out ? { date: out, hasDate: hasDate, hasTime: hasTime } : null;
  }

  /* ── completion ──────────────────────────────────────────────────
     The grammar above is exact, and says so: one word it does not know
     makes the whole reading null. That refusal is the promise which
     keeps this field from ever committing a date nobody typed, and it
     is not up for negotiation.

     Completion is the other half of that bargain. It never overrules a
     reading — an exact one wins outright and nothing below runs — it
     only offers one where the grammar had none. "5j" is not a date.
     "5 January", "5 June" and "5 July" are three, and putting all
     three on screen with their dates written out in full is precisely
     how a guess stops being a guess: the person reads the answer
     before they accept it.

     Nearest first, which is the whole of "the fifth of the next month
     starting with j": inferYear already rolls a bare day-and-month
     forward to its next occurrence, so ordering the readings by date
     IS that rule. Nothing here knows what a month is called. */

  // How many finishings one unfinished word may offer. A single letter
  // is the front of three months in English, and of more than that
  // once the weekdays and the vocabulary are in; a list nobody can
  // read at a glance has stopped being a suggestion.
  var MAX_COMPLETIONS = 6;

  // Every word in this locale's index that the run could still become.
  // A prefix, never an infix: people type the front of a word, so "ju"
  // finishes to June and July and "une" finishes to nothing.
  //
  // Two kinds are skipped. A literal is punctuation Intl threads
  // between numbers, which nobody is halfway through typing; and "at"
  // is optional everywhere it appears, so finishing a word INTO it
  // changes no reading and only spends a row.
  function completionsFor(run, idx) {
    var out = [], seen = {}, i, e;
    if (!run) return out;
    for (i = 0; i < idx.length; i++) {
      e = idx[i];
      if (e.role === "word" && (e.kind === "ignore" || e.kind === "at")) continue;
      if (e.text.length <= run.length) continue;
      if (e.text.slice(0, run.length) !== run) continue;
      if (seen[e.text]) continue;
      seen[e.text] = true;
      out.push(e.text);
    }
    return out;
  }

  // parseAll(text, vocab, tbl, now, ctx) -> [reading, ...], best first,
  // and empty where the grammar and the completions both come back
  // with nothing. This is what the field itself reads with; parse
  // stays exactly as strict as it was, and the read-back gate still
  // holds it to reading its own writing with no help from here.
  function parseAll(text, vocab, tbl, now, ctx) {
    var exact = parse(text, vocab, tbl, now, ctx);
    if (exact) return [exact];
    var raw = tbl.fold(text);
    if (!raw) return [];
    var idx = indexFor(vocab, tbl);
    var toks = scan(raw, idx), at = -1, i;
    for (i = 0; i < toks.length; i++) {
      if (toks[i].t !== "other") continue;
      // Two unfinished words is not a typo anybody is mid-way through,
      // it is prose — and finishing both would multiply out into a
      // list of dates nobody asked for.
      if (at >= 0) return [];
      at = i;
    }
    if (at < 0) return [];
    var names = completionsFor(toks[at].raw, idx), out = [], seen = {}, read, key;
    for (i = 0; i < names.length; i++) {
      // The sentence handed back to the grammar with that one run
      // finished, spaced off on both sides so a finished word cannot
      // glue itself onto its neighbour and mean something else.
      read = parse(raw.slice(0, toks[at].from) + " " + names[i] + " " + raw.slice(toks[at].to),
        vocab, tbl, now, ctx);
      if (!read) continue;
      key = String(read.date.getTime()) + (read.hasTime ? "t" : "");
      if (seen[key]) continue;
      seen[key] = true;
      out.push(read);
    }
    out.sort(function (a, b) { return a.date - b.date; });
    return out.slice(0, MAX_COMPLETIONS);
  }

  /* ── the page ────────────────────────────────────────────────────
     Everything below is behind the document guard, so Node loads the
     parser above and nothing else. */

  if (typeof document !== "undefined") {

    var CAL_SVG = '<svg viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" ' +
      'stroke-linecap="round" stroke-linejoin="round" aria-hidden="true" focusable="false">' +
      '<rect x="3" y="4" width="18" height="18" rx="2"/><path d="M16 2v4M8 2v4M3 10h18"/></svg>';

    // A time field's button opens a clock, not a calendar, and says so
    // with the one glyph everybody reads the same way.
    var CLOCK_SVG = '<svg viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" ' +
      'stroke-linecap="round" stroke-linejoin="round" aria-hidden="true" focusable="false">' +
      '<circle cx="12" cy="12" r="9"/><path d="M12 7v5l3 2"/></svg>';

    var seq = 0;

    function toWire(kind, d) {
      var day = d.getFullYear() + "-" + pad(d.getMonth() + 1) + "-" + pad(d.getDate());
      var clock = pad(d.getHours()) + ":" + pad(d.getMinutes());
      if (kind === "date") return day;
      if (kind === "time") return clock;
      return day + "T" + clock;
    }

    function fromWire(kind, value, now) {
      var s = String(value || ""), m;
      if (kind === "time") {
        m = /^(\d{2}):(\d{2})/.exec(s);
        return m ? new Date(now.getFullYear(), now.getMonth(), now.getDate(), +m[1], +m[2]) : null;
      }
      m = /^(\d{4})-(\d{2})-(\d{2})(?:[T ](\d{2}):(\d{2}))?/.exec(s);
      if (!m) return null;
      return new Date(+m[1], +m[2] - 1, +m[3], m[4] ? +m[4] : 0, m[5] ? +m[5] : 0);
    }

    // displayOptions is up in the pure half, because the parser reads
    // these back: one set of options, formatted here and derived there.
    function formatter(locale, kind) {
      try {
        return new Intl.DateTimeFormat(locale, displayOptions(kind));
      } catch (e) {
        return null;
      }
    }

    // Does this browser open a picker for this input? showPicker cannot
    // be asked: HTML has it RETURN silently — no error, no event —
    // where the browser has no picker for the type, which is exactly
    // how a button wired to it ends up looking like a control and doing
    // nothing. So the presence of the method is the only signal a page
    // gets, and every field that has it gets the button.
    //
    // Recorded rather than guarded, because it will be rediscovered
    // otherwise: measured on Firefox 152 (macOS), a date and a
    // datetime-local both open the panel and a TIME opens nothing. The
    // button is offered there anyway, on the same reasoning that lets a
    // time field share the calendar's label — one affordance, in one
    // place, on every date field. If a dead clock is reported, this
    // paragraph is where the fix starts.
    function canPick(el) { return !!el.showPicker; }

    function attr(el, name, fallback) {
      var v = el.getAttribute(name);
      return v === null || v === "" ? fallback : v;
    }

    function enhance(native) {
      if (native.dataset.rstEnhanced) return null; // idempotent: safe to re-scan
      native.dataset.rstEnhanced = "true";

      var kind = native.type === "time" ? "time" : (native.type === "date" ? "date" : "datetime");
      var locale = document.documentElement.getAttribute("lang") || undefined;
      var tbl = tables(locale);
      var fmt = formatter(locale, kind);
      var vocab = {};
      try {
        vocab = JSON.parse(attr(native, "data-rst-date-words", "{}")) || {};
      } catch (e) {
        vocab = {};
      }

      var id = native.id || "rst-dtp-" + ++seq;
      var listId = id + "-listbox";
      var setLabel = attr(native, "data-rst-date-set", "Set");
      var hintText = attr(native, "data-rst-date-hint", "");
      var pickLabel = attr(native, "data-rst-date-pick", "Open the calendar");
      var clockLabel = attr(native, "data-rst-date-pick-time", "Open the clock");
      var calLabel = attr(native, "data-rst-date-calendar", "Calendar");
      var prevLabel = attr(native, "data-rst-date-prev-month", "Previous month");
      var nextLabel = attr(native, "data-rst-date-next-month", "Next month");
      var manyFmt = attr(native, "data-rst-date-results", "{n} suggestions");
      var oneFmt = attr(native, "data-rst-date-result-one", "1 suggestion");
      var quick = {
        today: attr(native, "data-rst-date-quick-today", "Today"),
        tomorrow: attr(native, "data-rst-date-quick-tomorrow", "Tomorrow"),
        nextWeek: attr(native, "data-rst-date-quick-next-week", "In a week"),
        plus1h: attr(native, "data-rst-date-quick-plus-1h", "An hour later"),
        plus2h: attr(native, "data-rst-date-quick-plus-2h", "Two hours later"),
        endOfDay: attr(native, "data-rst-date-quick-end-of-day", "End of that day"),
        nextDay: attr(native, "data-rst-date-quick-next-day", "Same time next day")
      };

      var wrap = document.createElement("div");
      wrap.setAttribute("rst-dtp", "");

      var input = document.createElement("input");
      input.type = "text";
      input.setAttribute("rst-input", "");
      input.setAttribute("rst-dtp-input", "");
      input.id = id + "-combo";
      input.autocomplete = "off";
      input.spellcheck = false;
      input.setAttribute("role", "combobox");
      input.setAttribute("aria-expanded", "false");
      input.setAttribute("aria-controls", listId);
      input.setAttribute("aria-autocomplete", "list");

      var list = document.createElement("ul");
      list.setAttribute("rst-dtp-list", "");
      list.id = listId;
      list.hidden = true;
      list.setAttribute("role", "listbox");

      var status = document.createElement("p");
      status.className = "rst-sr-only";
      status.setAttribute("role", "status");
      status.setAttribute("aria-live", "polite");

      native.parentNode.insertBefore(wrap, native);
      wrap.appendChild(input);
      wrap.appendChild(list);
      wrap.appendChild(status);
      wrap.appendChild(native);

      // The label named the native input; it now names the control the
      // user actually types into. Without this the combobox has no
      // accessible name at all.
      var label = native.id ? document.querySelector('label[for="' + native.id + '"]') : null;
      if (label) label.setAttribute("for", input.id);
      else if (native.getAttribute("aria-label")) input.setAttribute("aria-label", native.getAttribute("aria-label"));
      if (native.getAttribute("aria-describedby"))
        input.setAttribute("aria-describedby", native.getAttribute("aria-describedby"));

      // Hidden, never removed: it is what the form submits.
      native.removeAttribute("rst-input");
      native.classList.add("rst-sr-only");
      native.setAttribute("tabindex", "-1");
      native.setAttribute("aria-hidden", "true");

      var rows = [];      // [{li, date, hasTime}] — selectable rows only
      var active = -1;
      var baseOf = null;  // range end: () => Date|null, the start's value
      var onCommit = null;
      var isEnd = false;
      var mine = false;   // true while WE are the ones writing to the carrier

      function current() { return fromWire(kind, native.value, new Date()); }
      function show(d) { return fmt ? fmt.format(d) : toWire(kind, d); }
      function paint() {
        var d = current();
        input.value = d ? show(d) : "";
      }

      function setActive(i) {
        active = i;
        for (var k = 0; k < rows.length; k++) rows[k].li.classList.toggle("is-active", k === i);
        if (i >= 0) input.setAttribute("aria-activedescendant", rows[i].li.id);
        else input.removeAttribute("aria-activedescendant");
      }

      // extra is the row's variant ("set", "alt") or a second attribute
      // ("rst-dtp-quick"): the vocabulary spells a variant in the value
      // slot and a kind as an attribute of its own, so one parameter
      // cannot be both, and the caller says which.
      function addRow(label, meta, date, hasTime, variant, extra) {
        var li = document.createElement("li");
        li.setAttribute("rst-dtp-row", variant || "");
        if (extra) li.setAttribute(extra, "");
        li.id = listId + "-" + rows.length;
        li.setAttribute("role", "option");
        // The row that IS the committed value says so in the
        // accessibility tree, which is what aria-selected means on a
        // listbox option — the highlight below is a separate thing,
        // moved by the arrow keys.
        li.setAttribute("aria-selected", toWire(kind, date) === native.value ? "true" : "false");
        var main = document.createElement("span");
        main.setAttribute("rst-dtp-label", "");
        main.textContent = label;
        li.appendChild(main);
        if (meta) {
          var m = document.createElement("span");
          m.setAttribute("rst-dtp-set", "");
          m.textContent = meta;
          li.appendChild(m);
        }
        li.addEventListener("mousedown", function (e) {
          e.preventDefault(); // keep focus; blur would undo the pick
          commit(date, hasTime);
        });
        list.appendChild(li);
        rows.push({ li: li, date: date, hasTime: hasTime });
      }

      function picks(now) {
        var start = baseOf ? baseOf() : null;
        var out = [], at;
        if (isEnd && start && kind !== "date") {
          return [
            { label: quick.plus1h, date: new Date(start.getTime() + 3600000) },
            { label: quick.plus2h, date: new Date(start.getTime() + 7200000) },
            { label: quick.endOfDay, date: new Date(start.getFullYear(), start.getMonth(), start.getDate(), 23, 59) },
            { label: quick.nextDay, date: new Date(start.getTime() + 86400000) }
          ];
        }
        if (kind === "time") {
          var from = current() || now;
          return [
            { label: quick.plus1h, date: new Date(from.getTime() + 3600000) },
            { label: quick.plus2h, date: new Date(from.getTime() + 7200000) },
            { label: quick.endOfDay, date: new Date(from.getFullYear(), from.getMonth(), from.getDate(), 23, 59) }
          ];
        }
        at = function (d) { return new Date(d.getFullYear(), d.getMonth(), d.getDate(), 9, 0); };
        out.push({ label: quick.today, date: at(now) });
        out.push({ label: quick.tomorrow, date: at(shiftDays(now, 1)) });
        out.push({ label: quick.nextWeek, date: at(shiftDays(now, 7)) });
        return out;
      }

      function ctx() {
        return { prev: current(), base: baseOf ? baseOf() : null };
      }

      function rebuild(query) {
        var now = new Date();
        list.textContent = "";
        rows = [];
        var text = String(query || "").trim();
        var reads = text ? parseAll(text, vocab, tbl, now, ctx()) : [];
        var i, skip = {}, wire;
        for (i = 0; i < reads.length; i++) {
          // The first reading is the one Enter takes, and it is
          // labelled as such. The rest are the other months a
          // half-typed word could still have meant — offered, never
          // chosen, each with its date written out in full, which is
          // what keeps a completion from being a guess.
          addRow(show(reads[i].date), i === 0 ? setLabel : "", reads[i].date, reads[i].hasTime,
            i === 0 ? "set" : "alt");
          skip[toWire(kind, reads[i].date)] = true;
        }
        var quickPicks = picks(now);
        for (i = 0; i < quickPicks.length; i++) {
          wire = toWire(kind, quickPicks[i].date);
          if (skip[wire]) continue;
          addRow(quickPicks[i].label, show(quickPicks[i].date), quickPicks[i].date, kind !== "date", "", "rst-dtp-quick");
        }
        if (hintText) {
          var hint = document.createElement("li");
          hint.setAttribute("rst-dtp-hint", "");
          hint.setAttribute("aria-hidden", "true");
          hint.textContent = hintText.replace("{example}", show(new Date(now.getFullYear(), now.getMonth(), now.getDate(), 9, 0)));
          list.appendChild(hint);
        }
        status.textContent = rows.length === 1 ? oneFmt : manyFmt.replace("{n}", String(rows.length));
        // Text that read as nothing leaves NOTHING armed: a quick pick
        // sitting under the cursor would make Enter on a date the
        // grammar did not understand commit today instead of refusing.
        setActive(rows.length && (reads.length || !text) ? 0 : -1);
      }

      // What the carrier itself allows, plus — for a range's end — the
      // start it must not come before. Asked fresh on every question
      // rather than captured once, because the start moves.
      function bounds() {
        var when = new Date();
        var lo = fromWire(kind, native.getAttribute("min"), when);
        var hi = fromWire(kind, native.getAttribute("max"), when);
        var start = baseOf ? baseOf() : null;
        if (isEnd && start && (!lo || start > lo)) lo = midnight(start);
        return { min: lo, max: hi };
      }

      // What the text in the box currently reads as, committing
      // nothing. Null where it reads as nothing at all.
      function previewOf(text) {
        var t = String(text || "").trim();
        if (!t) return null;
        var reads = parseAll(t, vocab, tbl, new Date(), ctx());
        return reads.length ? reads[0].date : null;
      }

      // The calendar overlay, when calendar.js is on the page. Looked
      // up here rather than at load time so the two files may arrive
      // in either order: both are deferred and this runs on
      // DOMContentLoaded, by which point both have executed.
      //
      // Absent, the button falls back to the browser's own panel —
      // the same posture this file takes towards a missing attribute.
      // A page that links one script and not the other gets the old
      // behaviour rather than a dead control.
      var makeCal = (typeof window !== "undefined" && window.rastrilloCalendar) || null;
      var cal = null;

      if (makeCal && kind !== "time") {
        cal = makeCal({
          id: id,
          locale: locale,
          labels: { calendar: calLabel, prev: prevLabel, next: nextLabel },
          value: current,
          bounds: bounds,
          onPick: function (day) {
            // A day chosen on the grid keeps the clock the field
            // already holds: picking the 5th for a meeting says
            // nothing about what time it starts, and throwing the time
            // away would be a second edit nobody asked for.
            var had = current();
            commit(new Date(day.getFullYear(), day.getMonth(), day.getDate(),
              had ? had.getHours() : 9, had ? had.getMinutes() : 0), kind !== "date");
          },
          onDismiss: function () {
            closeAll();
            input.focus();
          }
        });
        cal.el.id = id + "-cal";
        wrap.appendChild(cal.el);
      }

      var pick = null;
      var ownsPopup = !!cal || kind === "time";
      function pickExpanded(open) {
        if (pick && ownsPopup) pick.setAttribute("aria-expanded", open ? "true" : "false");
      }
      if (cal || kind === "time" || canPick(native)) {
        pick = document.createElement("button");
        pick.type = "button";
        pick.setAttribute("rst-dtp-pick", "");
        pick.setAttribute("aria-label", kind === "time" ? clockLabel : pickLabel);
        // Popup attributes only where the popup is ours: the grid is a
        // dialog and the clock is a listbox. Without either, the button
        // hands over to the browser's own panel, which is not a
        // listbox and whose open state this page never learns, so it
        // is announced as the plain button it is.
        if (ownsPopup) {
          pick.setAttribute("aria-expanded", "false");
          pick.setAttribute("aria-haspopup", cal ? "dialog" : "listbox");
        }
        pick.innerHTML = kind === "time" ? CLOCK_SVG : CAL_SVG;
        wrap.insertBefore(pick, list);
      }

      /* ── which popup is open ─────────────────────────────────────
         One region under the field, in one of three states, so there
         is only ever one thing to close, one thing the arrow keys
         drive, and one Escape:

           "list"   the suggestions — readings, quick picks, the hint
           "cal"    the month grid, on a date or datetime field
           "slots"  every half hour, on a time field

         The calendar REPLACES the suggestions rather than sitting
         beside them, because two popups under one field is two things
         the same keystroke could mean. The words keep working while it
         is open: typing moves the grid, and Enter still commits what
         the text reads as. */

      var mode = "";

      function closeAll() {
        mode = "";
        list.hidden = true;
        active = -1;
        input.setAttribute("aria-expanded", "false");
        input.setAttribute("aria-controls", listId);
        input.removeAttribute("aria-activedescendant");
        if (cal) cal.close();
        pickExpanded(false);
        wrap.classList.remove("is-above");
      }

      // Which side of the field the popup opens on. Below by default,
      // above where there is no room below and there is room above: a
      // panel hanging off the bottom of the window is a panel whose
      // last week nobody can click.
      function place() {
        var panel = mode === "cal" && cal ? cal.el : list;
        wrap.classList.remove("is-above");
        if (!panel || panel.hidden) return;
        var box = wrap.getBoundingClientRect();
        var need = panel.getBoundingClientRect().height;
        if (need > window.innerHeight - box.bottom && box.top > need) {
          wrap.classList.add("is-above");
        }
      }

      function openList() {
        if (cal) cal.close();
        mode = "list";
        list.hidden = false;
        input.setAttribute("aria-expanded", "true");
        input.setAttribute("aria-controls", listId);
        pickExpanded(false);
        rebuild(input.value);
        place();
      }

      function openCal() {
        list.hidden = true;
        active = -1;
        input.removeAttribute("aria-activedescendant");
        mode = "cal";
        // Opened on what the text already says, so somebody who typed
        // "next march" and then reached for the calendar arrives in
        // March rather than in this month.
        cal.open(previewOf(input.value));
        input.setAttribute("aria-expanded", "true");
        input.setAttribute("aria-controls", cal.el.id);
        pickExpanded(true);
        place();
      }

      // A time field has no calendar, so its button opens the clock:
      // every half hour of its own day, written in the field's own
      // locale and scrolled to the one it is already showing. The same
      // list element and the same keys as the suggestions — one popup,
      // one Escape, one set of arrows.
      function openSlots() {
        var base = current() || new Date();
        var day = midnight(base);
        var want = base.getHours() * 60 + base.getMinutes();
        var i, at, best = -1, near = 0, gap;
        mode = "slots";
        list.hidden = false;
        list.textContent = "";
        rows = [];
        for (i = 0; i < 48; i++) {
          at = new Date(day.getFullYear(), day.getMonth(), day.getDate(),
            Math.floor(i / 2), (i % 2) * 30);
          gap = Math.abs(at.getHours() * 60 + at.getMinutes() - want);
          if (best < 0 || gap < best) { best = gap; near = rows.length; }
          addRow(show(at), "", at, true, "", "rst-dtp-quick");
        }
        input.setAttribute("aria-expanded", "true");
        input.setAttribute("aria-controls", listId);
        pickExpanded(true);
        status.textContent = manyFmt.replace("{n}", String(rows.length));
        setActive(near);
        place();
        // The nearest half hour put where the eye lands. The list's own
        // scrollTop rather than scrollIntoView, which would also scroll
        // the page out from under the field.
        if (rows[near]) {
          list.scrollTop = rows[near].li.offsetTop - Math.round(list.clientHeight / 2);
        }
      }

      function commit(date, hasTime) {
        // Ours, so the change listener below leaves the repaint alone:
        // this path has already painted.
        mine = true;
        native.value = toWire(kind, date);
        native.dispatchEvent(new Event("change", { bubbles: true }));
        mine = false;
        input.value = show(date);
        closeAll();
        if (onCommit) onCommit(date, hasTime);
      }

      function commitText() {
        var text = input.value.trim();
        if (!text) {
          // Cleared on purpose: an emptied field empties the carrier,
          // and a required native then fails validation the way it
          // would with no script at all.
          input.value = "";
          if (native.value) {
            mine = true;
            native.value = "";
            native.dispatchEvent(new Event("change", { bubbles: true }));
            mine = false;
          }
          return;
        }
        var reads = parseAll(text, vocab, tbl, new Date(), ctx());
        if (reads.length) commit(reads[0].date, reads[0].hasTime);
        else paint(); // unreadable text is not a value: put the old one back
      }

      input.addEventListener("focus", function () {
        input.select();
        if (!mode) openList();
      });
      input.addEventListener("click", function () {
        if (!mode) openList();
      });
      input.addEventListener("input", function () {
        // The live half of the brief: while the grid is up, the words
        // drive it. Every keystroke re-reads the text and moves the
        // calendar to what it now says, committing nothing and taking
        // focus out of nothing.
        if (mode === "cal" && cal) return cal.show(previewOf(input.value));
        openList();
      });
      input.addEventListener("keydown", function (e) {
        switch (e.key) {
          case "ArrowDown":
            e.preventDefault();
            // Down out of the box is down into whatever is open: the
            // grid takes focus, and the arrow keys become days.
            if (mode === "cal" && cal) { cal.focusGrid(); break; }
            if (!mode) { openList(); break; }
            if (rows.length) setActive(Math.min(active + 1, rows.length - 1));
            break;
          case "ArrowUp":
            e.preventDefault();
            if (mode === "cal" && cal) { cal.focusGrid(); break; }
            if (!mode) { openList(); break; }
            if (rows.length) setActive(Math.max(active - 1, 0));
            break;
          case "Enter":
            e.preventDefault();
            if (mode !== "cal" && !list.hidden && active >= 0) commit(rows[active].date, rows[active].hasTime);
            else { commitText(); closeAll(); }
            break;
          case "Escape":
            e.preventDefault();
            paint();
            closeAll();
            break;
        }
      });

      // Enter belongs to the combobox, never to the form: a commit that
      // also submits the page throws the commit away and posts a value
      // nobody finished choosing. The keydown handler above already
      // calls preventDefault, which normally stops this event being
      // generated at all — it is stopped here as well because a
      // SYNTHETIC key injection (CDP, and every browser drive built on
      // it) delivers keydown and keypress as two independent events,
      // so the second one arrives with the first one's refusal
      // discarded. Measured, not guessed: a keypress listener on the
      // page reports defaultPrevented=false for exactly that Enter.
      input.addEventListener("keypress", function (e) {
        if (e.key === "Enter") e.preventDefault();
      });

      // Focus left the field, so commit what is in the box and shut
      // the popup — but only if it really left.
      //
      // The check is deferred by a tick, and that tick is the whole
      // point. Paging the calendar REBUILDS the grid, which removes the
      // very cell that had focus; the browser fires focusout with a
      // null relatedTarget on the way out and calendar.js puts focus on
      // the new cell immediately afterwards. Reading relatedTarget
      // alone, every arrow key inside the grid looked like a person
      // walking away: the field committed whatever the box happened to
      // say and slammed the panel shut under the cursor. A tick later
      // the answer is not in doubt — document.activeElement is either
      // back inside this field or it is not.
      wrap.addEventListener("focusout", function () {
        setTimeout(function () {
          if (wrap.contains(document.activeElement)) return;
          commitText();
          closeAll();
        }, 0);
      });

      if (pick) {
        // The press must not blur the box: a blur commits the text,
        // and not committing it yet is the whole reason the button is
        // being pressed.
        pick.addEventListener("mousedown", function (e) { e.preventDefault(); });
        pick.addEventListener("click", function () {
          if (mode === "cal" || mode === "slots") {
            closeAll();
            input.focus();
            return;
          }
          input.focus();
          if (cal) return openCal();
          if (kind === "time") return openSlots();
          // Neither a calendar nor a clock, which means calendar.js is
          // not on the page. The browser's own panel is better than a
          // button that does nothing, and this is the only line in
          // this file that opens it.
          closeAll();
          try {
            native.showPicker();
          } catch (err) {
            /* no user gesture, or no picker — nothing to break */
          }
        });
      }

      // The carrier changed underneath us: a pick in the browser's own
      // panel, a range's start seeding its end, an app's own script.
      // Repaint, so the box shows what the form will actually post.
      //
      // The guard here used to be "unless the combobox has focus",
      // which silently swallowed the one case it most needed to catch.
      // The native panel opens WITHOUT moving focus, so a date picked
      // in it never reached the box and the field looked like it had
      // ignored the selection. It now skips only our own commits, which
      // have painted already, and a change that arrives while somebody
      // is genuinely mid-word with a popup open.
      native.addEventListener("change", function () {
        if (mine) return;
        if (mode && document.activeElement === input) return;
        paint();
      });

      paint();

      return {
        get value() { return current(); },
        commit: commit,
        follow: function (fn) { baseOf = fn; isEnd = true; },
        committed: function (fn) { onCommit = fn; }
      };
    }

    // Two armed inputs inside one [data-rst-range] are a range: first
    // is the start, second the end, by DOM order. The end's quick picks
    // are relative to the start's current value, and a time typed into
    // the end with no date lands on the start's day.
    function wireRange(wrapper, start, end) {
      end.follow(function () { return start.value; });
      if (wrapper.getAttribute("data-rst-range") !== "session") return;
      // "session" ranges (a talk, a meeting) seed an empty or backwards
      // end to an hour later. Server-side nothing is seeded, so a
      // submission with scripts off is exactly what was typed.
      start.committed(function (d) {
        var have = end.value;
        if (!have || have <= d) end.commit(new Date(d.getTime() + 3600000), true);
      });
    }

    function scanPage(root) {
      var armed = root.querySelectorAll("input[data-rst-date], input[data-rst-time]");
      var made = [], i, k, wrappers, inside, pair;
      for (i = 0; i < armed.length; i++) made.push([armed[i], enhance(armed[i])]);
      wrappers = root.querySelectorAll("[data-rst-range]");
      for (i = 0; i < wrappers.length; i++) {
        inside = wrappers[i].querySelectorAll("input[data-rst-date], input[data-rst-time]");
        pair = [];
        for (k = 0; k < inside.length && pair.length < 2; k++) {
          for (var j = 0; j < made.length; j++)
            if (made[j][0] === inside[k] && made[j][1]) pair.push(made[j][1]);
        }
        if (pair.length === 2) wireRange(wrappers[i], pair[0], pair[1]);
      }
    }

    if (document.readyState === "loading") {
      document.addEventListener("DOMContentLoaded", function () { scanPage(document); });
    } else {
      scanPage(document);
    }
  }

  // The pure half, for ui/datetime_node.mjs. A browser has no `module`
  // and never takes this branch.
  if (typeof module !== "undefined" && module && module.exports) {
    module.exports = {
      parse: parse,
      parseAll: parseAll,
      tables: tables,
      displayOptions: displayOptions
    };
  }
})();
