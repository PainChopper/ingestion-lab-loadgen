(() => {
    const STATUS_UPDATE_MS = 250;

    // Piecewise logarithmic scale.
    // Left half: 1 .. 100K
    // Right half: 100K .. 1M
    const LOG_BASE = 10;
    const LOG_BREAK_POS = 50000;
    const LOG_BREAK_TPS = 100000;
    const LOG_MAX_POS = 100000;
    const LOG_MAX_TPS = 1000000;
    const LOG_SCALE_LOW = 10000;
    const LOG_SCALE_HIGH = 50000;
    const LOG_BASE_LN = Math.log(LOG_BASE);
    const LOG_BREAK_LN = Math.log(LOG_BREAK_TPS);

    // Tick styling / density.
    // The left side stays relatively sparse, the right side becomes much denser.
    const MAJOR_CLEARANCE_PX = 7;
    const SEGMENT_GAP_START_PX = 100;
    const SEGMENT_GAP_END_PX = 7;
    const SEGMENT_GAP_POWER = 0.92;

    function positionToTps(pos) {
        let tps;
        if (pos <= LOG_BREAK_POS) {
            tps = Math.pow(LOG_BASE, pos / LOG_SCALE_LOW);
        } else {
            const exp = (LOG_BREAK_LN / LOG_BASE_LN) + ((pos - LOG_BREAK_POS) / LOG_SCALE_HIGH);
            tps = Math.pow(LOG_BASE, exp);
        }
        return Math.max(1, Math.min(LOG_MAX_TPS, tps));
    }

    function tpsToPosition(tps) {
        const clampedTps = Math.max(1, Math.min(LOG_MAX_TPS, tps));
        let pos;
        if (clampedTps <= LOG_BREAK_TPS) {
            pos = LOG_SCALE_LOW * Math.log(clampedTps) / LOG_BASE_LN;
        } else {
            pos = LOG_BREAK_POS + LOG_SCALE_HIGH * ((Math.log(clampedTps) - LOG_BREAK_LN) / LOG_BASE_LN);
        }
        return Math.max(0, Math.min(LOG_MAX_POS, pos));
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

    function formatTps(tps) {
        return Math.round(tps).toLocaleString();
    }

    function setStatus(text, isError = false) {
        els.status.textContent = text;
        els.status.classList.toggle("err", isError);
        els.status.classList.toggle("muted", !isError);
    }

    function parseTpsToValue(tpsStr) {
        if (!tpsStr) return 0;

        const s = String(tpsStr).trim();
        const m = s.match(/^(\d+)\s*tps$/i);
        if (m) return parseInt(m[1], 10);

        const n = parseInt(s, 10);
        return Number.isNaN(n) ? 0 : n;
    }

    function getTrackMetrics(container) {
        const ticksLayer = container.querySelector(".slider-ticks");
        const marks = Array.from(container.querySelectorAll(".slider-marks .mark"));

        if (!ticksLayer || marks.length === 0) return null;

        const layerRect = ticksLayer.getBoundingClientRect();
        const width = layerRect.width;
        if (width <= 0) return null;

        const majorXs = marks
            .map((mark) => {
                const rect = mark.getBoundingClientRect();
                return rect.left + rect.width / 2 - layerRect.left;
            })
            .map((x) => Math.max(0, Math.min(width, x)));

        return { ticksLayer, width, majorXs };
    }

    function renderTick(ticksLayer, xPx, widthPx, isMajor) {
        const tick = document.createElement("span");
        tick.className = `tick ${isMajor ? "major" : "minor"}`;
        tick.style.left = `${(xPx / widthPx) * 100}%`;
        ticksLayer.appendChild(tick);
    }

    function buildMinorXs(majorXs) {
        const minorXs = [];
        const segmentCount = Math.max(0, majorXs.length - 1);

        for (let i = 0; i < segmentCount; i += 1) {
            const start = majorXs[i];
            const end = majorXs[i + 1];
            const span = end - start;
            if (span <= 0) continue;

            const segmentT = segmentCount <= 1 ? 1 : i / (segmentCount - 1);
            const desiredGap =
                SEGMENT_GAP_START_PX -
                (SEGMENT_GAP_START_PX - SEGMENT_GAP_END_PX) * Math.pow(segmentT, SEGMENT_GAP_POWER);

            const usableStart = start + MAJOR_CLEARANCE_PX;
            const usableEnd = end - MAJOR_CLEARANCE_PX;
            const usableSpan = usableEnd - usableStart;

            if (usableSpan <= desiredGap) continue;

            const count = Math.max(1, Math.floor(usableSpan / desiredGap));

            for (let j = 1; j <= count; j += 1) {
                const x = usableStart + (usableSpan * j) / (count + 1);
                minorXs.push(x);
            }
        }

        return minorXs;
    }

    function buildTicksForSlider(sliderEl) {
        const container = sliderEl.closest(".slider-container");
        if (!container) return;

        const metrics = getTrackMetrics(container);
        if (!metrics) return;

        const { ticksLayer, width, majorXs } = metrics;
        ticksLayer.textContent = "";

        const minorXs = buildMinorXs(majorXs);

        for (const x of minorXs) {
            renderTick(ticksLayer, x, width, false);
        }

        for (const x of majorXs) {
            renderTick(ticksLayer, x, width, true);
        }
    }

    function rebuildAllTicks() {
        buildTicksForSlider(els.slider);
        buildTicksForSlider(els.actualSlider);
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

            if (!suppressSliderSync) {
                const tpsRaw = parseTpsToValue(data.targetTPS);
                const tps = Math.max(1, Math.min(LOG_MAX_TPS, tpsRaw));
                const sliderPos = tpsToPosition(tps);
                els.slider.value = Math.round(sliderPos);
                els.display.textContent = `${formatTps(tps)} TPS`;
            }

            const actualTpsRaw = parseTpsToValue(data.actualTPS);
            const actualTps = Math.max(1, Math.min(LOG_MAX_TPS, actualTpsRaw));
            const actualSliderPos = tpsToPosition(actualTps);
            els.actualSlider.value = Math.round(actualSliderPos);
            els.actualDisplay.textContent = `${formatTps(actualTps)} TPS`;

            els.totalMetric.textContent = data.totalTransactions;
            setStatus("ok");
        } catch (e) {
            setStatus(`connection error: ${e.message}`, true);
        } finally {
            statusInFlight = false;
        }
    }

    els.slider.addEventListener("input", () => {
        suppressSliderSync = true;
        const sliderPos = parseFloat(els.slider.value);
        const tps = positionToTps(sliderPos);
        els.display.textContent = `${formatTps(tps)} TPS`;
    });

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

    const resizeObserver = new ResizeObserver(() => rebuildAllTicks());
    resizeObserver.observe(document.body);

    window.addEventListener("load", () => {
        rebuildAllTicks();
        loadStatus();
        startStatusUpdates();
    });

    window.addEventListener("resize", rebuildAllTicks);

    rebuildAllTicks();
})();