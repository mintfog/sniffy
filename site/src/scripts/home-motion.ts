const reducedMotion = window.matchMedia("(prefers-reduced-motion: reduce)");
const compactViewport = window.matchMedia("(max-width: 760px)");
const pendingReveals = new Set(
  document.querySelectorAll<HTMLElement>("[data-reveal]"),
);
const scenes = document.querySelectorAll<HTMLElement>("[data-motion-scene]");
const animations = new Map<HTMLElement, Animation>();
const visibleScenes = new Set<HTMLElement>();

function syncPlayback() {
  const playing = !document.hidden && !reducedMotion.matches;
  scenes.forEach((scene) => {
    scene.dataset.motionActive = String(playing && visibleScenes.has(scene));
  });
  animations.forEach((animation) => {
    if (playing) animation.play();
    else animation.pause();
  });
}

// 正文默认可见，确保脚本不可用时仍可阅读。
if ("IntersectionObserver" in window && "animate" in Element.prototype) {
  const revealObserver = new IntersectionObserver(
    (entries) => {
      for (const entry of entries) {
        if (!entry.isIntersecting || reducedMotion.matches) continue;
        const element = entry.target as HTMLElement;
        revealObserver.unobserve(element);
        pendingReveals.delete(element);
        if (element.contains(document.activeElement)) continue;

        const compact = compactViewport.matches;
        const delay = Math.min(
          Number(element.dataset.revealDelay) || 0,
          compact ? 60 : 240,
        );
        const animation = element.animate(
          [
            { opacity: 0, transform: `translateY(${compact ? 12 : 20}px)` },
            { opacity: 1, transform: "translateY(0)" },
          ],
          {
            duration: compact ? 480 : 640,
            delay,
            easing: "cubic-bezier(0.22, 1, 0.36, 1)",
            fill: "backwards",
          },
        );
        animations.set(element, animation);
        animation.onfinish = animation.oncancel = () => {
          animations.delete(element);
        };
      }
      syncPlayback();
    },
    { threshold: 0.08 },
  );

  const sceneObserver = new IntersectionObserver(
    (entries) => {
      for (const entry of entries) {
        const scene = entry.target as HTMLElement;
        if (entry.isIntersecting) {
          visibleScenes.add(scene);
          scene.dataset.motionReady = "true";
        } else {
          visibleScenes.delete(scene);
        }
      }
      syncPlayback();
    },
    { threshold: 0.15 },
  );

  function syncPreference() {
    if (reducedMotion.matches) {
      revealObserver.disconnect();
      sceneObserver.disconnect();
      animations.forEach((animation) => animation.cancel());
      animations.clear();
      visibleScenes.clear();
    } else {
      pendingReveals.forEach((element) => revealObserver.observe(element));
      scenes.forEach((scene) => sceneObserver.observe(scene));
    }
    syncPlayback();
  }

  // 键盘跳到正在入场的控件时立即呈现，避免焦点落在透明内容上。
  document.addEventListener("focusin", (event) => {
    if (!(event.target instanceof Node)) return;
    for (const [element, animation] of animations) {
      if (element.contains(event.target)) animation.cancel();
    }
  });
  document.addEventListener("visibilitychange", syncPlayback);
  reducedMotion.addEventListener("change", syncPreference);
  syncPreference();
}
