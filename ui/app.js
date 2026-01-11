(() => {
    const DEBOUNCE_MS = 750;

    const els = {
        radios: Array.from(document.getElementsByName("mode")),
        slider: document.getElementById("pulse-slider"),
        display: document.getElementById("pulse-display"),
        quitBtn: document.getElementById("quit-btn"),
        status: document.getElementById("status-area"),
    };

    let pulseTimer = null;
    let suppressSliderSync = false;

    function clampInt(v, min, max) {
        if (!Number.isFinite(v)) return min;
        return Math.min(max, Math.max(min, v | 0));
    }

    function parsePulseToMs(pulseStr) {
        if (!pulseStr) return 0;

        const s = String(pulseStr).trim();

        // "123ms"
        let m = s.match(/^(\d+)\s*ms$/i);
        if (m) return parseInt(m[1], 10);

        // "1s" or "0.5s"
        m = s.match(/^(\d+(?:\.\d+)?)\s*s$/i);
        if (m) return Math.round(parseFloat(m[1]) * 1000);

        // fallback: try parse integer as ms
        const n = parseInt(s, 10);
        return Number.isNaN(n) ? 0 : n;
    }

    function sliderMin() { return parseInt(els.slider.min, 10) || 1; }
    function sliderMax() { return parseInt(els.slider.max, 10) || 2000; }

    function setStatus(text, isError = false) {
        els.status.textContent = text;
        els.status.classList.toggle("err", isError);
        els.status.classList.toggle("muted", !isError);
    }

    async function postControl(action, value) {
        const res = await fetch("/control", {
            method: "POST",
            headers: { "Content-Type": "application/json" },
            body: JSON.stringify({ action, value }),
        });
        if (!res.ok) throw new Error(`HTTP ${res.status}`);
    }

    async function loadStatus() {
        try {
            const res = await fetch("/status");
            if (!res.ok) throw new Error(`HTTP ${res.status}`);
            const data = await res.json();

            // Mode radios
            for (const r of els.radios) {
                r.checked = (r.value === data.mode);
            }

            // Pulse slider (do not fight the user while dragging)
            if (!suppressSliderSync) {
                const msRaw = parsePulseToMs(data.pulse);
                const ms = clampInt(msRaw, sliderMin(), sliderMax());
                els.slider.value = String(ms);
                els.display.textContent = `${ms}ms`;
            }

            setStatus(`ok\nmode:  ${data.mode}\npulse: ${data.pulse}\nlimit: ${data.limit}`);
        } catch (e) {
            setStatus(`connection error: ${e.message}`, true);
        }
    }

    // Mode change -> immediate POST
    for (const r of els.radios) {
        r.addEventListener("change", async (e) => {
            if (!e.target.checked) return;
            try {
                await postControl("mode", e.target.value);
                await loadStatus();
            } catch (err) {
                setStatus(`mode error: ${err.message}`, true);
            }
        });
    }

    // Pulse slider -> debounce POST
    els.slider.addEventListener("input", () => {
        suppressSliderSync = true;

        const ms = clampInt(parseInt(els.slider.value, 10), sliderMin(), sliderMax());
        els.display.textContent = `${ms}ms`;

        if (pulseTimer) clearTimeout(pulseTimer);

        pulseTimer = setTimeout(async () => {
            pulseTimer = null;
            suppressSliderSync = false;

            const ms2 = clampInt(parseInt(els.slider.value, 10), sliderMin(), sliderMax());
            try {
                await postControl("pulse", `${ms2}ms`);
                await loadStatus();
            } catch (err) {
                setStatus(`pulse error: ${err.message}`, true);
            }
        }, DEBOUNCE_MS);
    });

    // Quit
    els.quitBtn.addEventListener("click", async () => {
        try {
            await postControl("quit", "");
            setStatus("quit sent");
            els.slider.disabled = true;
            for (const r of els.radios) r.disabled = true;
            els.quitBtn.disabled = true;
        } catch (err) {
            setStatus(`quit error: ${err.message}`, true);
        }
    });

    // Init: run immediately (script is loaded after DOM)
    loadStatus();
})();
