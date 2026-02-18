(() => {
    const DEBOUNCE_MS = 750;
    const STATUS_UPDATE_MS = 1000; 

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

    let tpsTimer = null;
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
        if (statusInFlight) return;
        statusInFlight = true;
        try {
            const res = await fetch("/status");
            if (!res.ok) {
                const text = await res.text();
                throw new Error(`HTTP ${res.status}: ${text}`);
            }
            const data = await res.json();
            console.log('Server response:', data); // Debug

            // Target TPS slider (do not fight the user while dragging)
            if (!suppressSliderSync) {
                const tpsRaw = parseTpsToValue(data.targetTPS);
                const tps = clampInt(tpsRaw, sliderMin(), sliderMax());
                els.slider.value = String(tps);
                els.display.textContent = `${tps} TPS`;
            }

            // Actual TPS slider (always update from server)
            const actualTpsRaw = parseTpsToValue(data.actualTPS);
            const actualTps = clampInt(actualTpsRaw, sliderMin(), sliderMax());
            els.actualSlider.value = String(actualTps);
            els.actualDisplay.textContent = `${actualTps} TPS`;

            // Update total transactions metric
            els.totalMetric.textContent = data.totalTransactions;

            setStatus("ok");
        } catch (e) {
            setStatus(`connection error: ${e.message}`, true);
        } finally {
            statusInFlight = false;
        }
    }

    
    // Target TPS slider -> debounce POST
    els.slider.addEventListener("input", () => {
        suppressSliderSync = true;

        const tps = clampInt(parseInt(els.slider.value, 10), sliderMin(), sliderMax());
        els.display.textContent = `${tps} TPS`;

        if (tpsTimer) clearTimeout(tpsTimer);

        tpsTimer = setTimeout(async () => {
            tpsTimer = null;
            suppressSliderSync = false;

            if (els.slider.disabled) return; // Don't send if quit was pressed

            const tps2 = clampInt(parseInt(els.slider.value, 10), sliderMin(), sliderMax());
            try {
                await postControl("targetTPS", `${tps2}`);
                await loadStatus();
            } catch (err) {
                setStatus(`tps error: ${err.message}`, true);
            }
        }, DEBOUNCE_MS);
    });

    // Quit
    els.quitBtn.addEventListener("click", async () => {
        // Clear any pending Target TPS slider updates
        if (tpsTimer) {
            clearTimeout(tpsTimer);
            tpsTimer = null;
        }
        
        try {
            await postControl("quit", "");
            setStatus("quit sent");
            els.slider.disabled = true;
            els.actualSlider.disabled = true;
            els.quitBtn.disabled = true;
            stopStatusUpdates(); // Stop periodic updates when quitting
        } catch (err) {
            setStatus(`quit error: ${err.message}`, true);
        }
    });

    // Start periodic status updates
    function startStatusUpdates() {
        if (statusUpdateTimer) clearInterval(statusUpdateTimer);
        statusUpdateTimer = setInterval(loadStatus, STATUS_UPDATE_MS);
    }

    // Stop periodic status updates
    function stopStatusUpdates() {
        if (statusUpdateTimer) {
            clearInterval(statusUpdateTimer);
            statusUpdateTimer = null;
        }
    }

    // Init: run immediately and start periodic updates
    loadStatus();
    startStatusUpdates();
})();
