(() => {
    const STATUS_UPDATE_MS = 250;

    // Logarithmic scale parameters
    const LOG_BASE = 10;
    const LOG_SCALE = 100000 / 6; // ~16666.67, so pos=0→1 TPS, pos=100000→1e6 TPS

    // Logarithmic mapping functions
    function positionToTps(pos) {
        const tps = Math.pow(LOG_BASE, pos / LOG_SCALE);
        return Math.max(1, Math.min(1000000, tps)); // clamp to [1, 1e6]
    }

    function tpsToPosition(tps) {
        const clampedTps = Math.max(1, Math.min(1000000, tps)); // clamp to [1, 1e6]
        const pos = LOG_SCALE * Math.log(clampedTps) / Math.log(LOG_BASE);
        return Math.max(0, Math.min(100000, pos)); // clamp to slider range
    }

    function requireEl(id) {
        const el = document.getElementById(id);
        if (!el) throw new Error(`Missing element #${id}`);
        return el;
    }

    const els = {
        slider: requireEl("tps-slider"),
        display: requireEl("tps-display"),
        actualSlider: requireEl("actual-tps-slider"),
        actualDisplay: requireEl("actual-tps-display"),
        totalMetric: requireEl("total-metric"),
        quitBtn: requireEl("quit-btn"),
        status: requireEl("status-area"),
    };

    let suppressSliderSync = false;
    let statusInFlight = false;
    let statusUpdateTimer = null;

    function clampInt(v, min, max) {
        const n = Number(v);
        if (!Number.isFinite(n)) return min;
        const i = Math.trunc(n);
        return Math.min(max, Math.max(min, i));
    }

    function parseTpsToValue(tpsStr) {
        if (!tpsStr) return 0;

        const s = String(tpsStr).trim();

        // "123tps"
        let m = s.match(/^(\d+)\s*tps$/i);
        if (m) return parseInt(m[1], 10);

        // fallback: try parse integer
        const n = parseInt(s, 10);
        return Number.isNaN(n) ? 0 : n;
    }

    const SLIDER_MIN = parseInt(els.slider.min, 10) || 0;
    const SLIDER_MAX = parseInt(els.slider.max, 10) || 100000;

    function sliderMin() { return SLIDER_MIN; }
    function sliderMax() { return SLIDER_MAX; }

    function formatTps(tps) {
        return Math.round(tps).toLocaleString();
    }

    function setStatus(text, isError = false) {
        els.status.textContent = text;
        els.status.classList.toggle("err", isError);
        els.status.classList.toggle("muted", !isError);
    }

    async function postControl(action, value) {
        console.log(`Sending control: ${action} = ${value}`);
        const res = await fetch("/control", {
            method: "POST",
            headers: { "Content-Type": "application/json" },
            body: JSON.stringify({ action, value }),
        });
        if (!res.ok) throw new Error(`HTTP ${res.status}`);
    }

    async function loadStatus() {
        if (statusInFlight) return;
        statusInFlight = true;
        try {
            const res = await fetch("/status");
            if (!res.ok) {
                const text = await res.text();
                throw new Error(`HTTP ${res.status}: ${text}`);
            }
            const data = await res.json();
            console.log('Status response:', data);

            // Target TPS slider (do not fight the user while dragging)
            if (!suppressSliderSync) {
                const tpsRaw = parseTpsToValue(data.targetTPS);
                const tps = Math.max(1, Math.min(1000000, tpsRaw)); // clamp to [1, 1e6]
                const sliderPos = tpsToPosition(tps);
                console.log(`Target: server=${tpsRaw} -> clamped=${tps} -> sliderPos=${sliderPos}`);
                els.slider.value = Math.round(sliderPos);
                els.display.textContent = `${formatTps(tps)} TPS`;
            }

            // Actual TPS slider (always update from server)
            const actualTpsRaw = parseTpsToValue(data.actualTPS);
            const actualTps = Math.max(1, Math.min(1000000, actualTpsRaw)); // clamp to [1, 1e6]
            const actualSliderPos = tpsToPosition(actualTps);
            console.log(`Actual: server=${actualTpsRaw} -> clamped=${actualTps} -> sliderPos=${actualSliderPos}`);
            els.actualSlider.value = Math.round(actualSliderPos);
            els.actualDisplay.textContent = `${formatTps(actualTps)} TPS`;

            // Update total transactions metric
            els.totalMetric.textContent = data.totalTransactions;

            setStatus("ok");
        } catch (e) {
            setStatus(`connection error: ${e.message}`, true);
        } finally {
            statusInFlight = false;
        }
    }

    // Target TPS slider: update label while dragging
    els.slider.addEventListener("input", () => {
        suppressSliderSync = true;

        const sliderPos = parseFloat(els.slider.value);
        const tps = positionToTps(sliderPos);
        els.display.textContent = `${formatTps(tps)} TPS`;
    });

    // Apply Target TPS immediately when the user finishes the change
    els.slider.addEventListener("change", async () => {
        if (els.slider.disabled) return;

        const sliderPos = parseFloat(els.slider.value);
        const tps = positionToTps(sliderPos);

        try {
            await postControl("targetTPS", `${Math.round(tps)}`);
            suppressSliderSync = false;
            await loadStatus();
        } catch (err) {
            suppressSliderSync = false;
            setStatus(`tps error: ${err.message}`, true);
        }
    });

    // Quit
    els.quitBtn.addEventListener("click", async () => {
        try {
            await postControl("quit", "");
            setStatus("quit sent");
            els.slider.disabled = true;
            els.actualSlider.disabled = true;
            els.quitBtn.disabled = true;
            stopStatusUpdates();
        } catch (err) {
            setStatus(`quit error: ${err.message}`, true);
        }
    });

    function startStatusUpdates() {
        if (statusUpdateTimer) clearInterval(statusUpdateTimer);
        statusUpdateTimer = setInterval(loadStatus, STATUS_UPDATE_MS);
    }

    function stopStatusUpdates() {
        if (statusUpdateTimer) {
            clearInterval(statusUpdateTimer);
            statusUpdateTimer = null;
        }
    }

    loadStatus();
    startStatusUpdates();
})();
