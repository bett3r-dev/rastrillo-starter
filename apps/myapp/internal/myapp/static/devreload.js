/* devreload.js — make dev only (MYAPP_DEV_RELOAD; see devreload.go).
   Polls /dev/boot and reloads the page once a different process is
   serving, i.e. after rastrillo dev rebuilt and restarted the app. A
   failed poll is the restart in progress: keep trying. A background
   tab keeps polling (the browser throttles it), so it is already fresh
   when you switch back; a prerendered page waits until it is shown. */
(() => {
  const script = document.currentScript;
  const booted = script && script.dataset.boot;
  if (!booted) return;
  const tick = async () => {
    try {
      const r = await fetch("/dev/boot", { cache: "no-store" });
      if (r.ok && (await r.text()) !== booted) {
        location.reload();
        return;
      }
    } catch (_) {
      /* the server is restarting */
    }
    setTimeout(tick, 400);
  };
  if (document.prerendering) {
    document.addEventListener("prerenderingchange", tick, { once: true });
  } else {
    tick();
  }
})();
