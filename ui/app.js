const state = {
    pulseTimer: null,
    sliderMin: 50
};

const els = {
    radios: document.getElementsByName('mode'),
    slider: document.getElementById('pulse-slider'),
    display: document.getElementById('pulse-display'),
    quitBtn: document.getElementById('quit-btn'),
    status: document.getElementById('status-area')
};

// --- Utils ---

// Parse Go duration string (e.g. "100ms", "2s", "1.5s") to milliseconds
function parseDuration(str) {
    if (!str) return 0;
    
    // Try milliseconds
    const msMatch = str.match(/^(\d+)ms$/);
    if (msMatch) return parseInt(msMatch[1], 10);

    // Try seconds (including float)
    const sMatch = str.match(/^(\d+(\.\d+)?)s$/);
    if (sMatch) return Math.round(parseFloat(sMatch[1]) * 1000);

    // Fallback: simple integer parse assuming ms if no unit, or just robust fallback
    const simple = parseInt(str, 10);
    return isNaN(simple) ? 0 : simple;
}

function updateStatus(text, isError = false) {
    els.status.textContent = text;
    els.status.style.color = isError ? '#d32f2f' : '#555';
}

async function apiCall(action, value) {
    try {
        const res = await fetch('/control', {
            method: 'POST',
            headers: { 'Content-Type': 'application/json' },
            body: JSON.stringify({ action, value })
        });
        if (!res.ok) throw new Error(`HTTP ${res.status}`);
        
        // Refresh status after command to ensure sync
        if (action !== 'quit') {
            setTimeout(loadStatus, 200);
        }
    } catch (e) {
        updateStatus(`Error sending ${action}: ${e.message}`, true);
    }
}

async function loadStatus() {
    try {
        const res = await fetch('/status');
        if (!res.ok) throw new Error(`HTTP ${res.status}`);
        const data = await res.json();
        
        // Update UI from status
        // Mode
        for (const r of els.radios) {
            if (r.value === data.mode) r.checked = true;
        }
        
        // Pulse
        const ms = parseDuration(data.pulse);
        if (ms > 0) {
            // Only update slider if not currently dragging (optional, but safer to just update)
            // For simplicity in this "minimal" version, we update it unless user is actively interacting?
            // Actually, requirements say "loads current status... initializes controls".
            // We'll update it. If user is dragging, debounce handles the set, but incoming status might jump.
            // Given the flow, we usually only fetch on load or after set.
            els.slider.value = ms;
            els.display.textContent = `${ms}ms`;
        }
        
        updateStatus(`Connected. Limit: ${data.limit} | Mode: ${data.mode} | Pulse: ${data.pulse}`);
    } catch (e) {
        updateStatus(`Connection lost: ${e.message}`, true);
    }
}

// --- Event Listeners ---

// Mode
els.radios.forEach(r => {
    r.addEventListener('change', (e) => {
        if (e.target.checked) {
            apiCall('mode', e.target.value);
        }
    });
});

// Pulse
els.slider.addEventListener('input', (e) => {
    const val = parseInt(e.target.value, 10);
    els.display.textContent = `${val}ms`;
    
    // Debounce
    if (state.pulseTimer) clearTimeout(state.pulseTimer);
    
    state.pulseTimer = setTimeout(() => {
        if (val <= 0) return; // Safety check
        apiCall('pulse', `${val}ms`);
    }, 700);
});

// Quit
els.quitBtn.addEventListener('click', () => {
    if (confirm('Stop the load generator server?')) {
        apiCall('quit', '');
        updateStatus('Server stopping...');
        // Disable controls
        els.slider.disabled = true;
        els.radios.forEach(r => r.disabled = true);
        els.quitBtn.disabled = true;
    }
});

// Init
document.addEventListener('DOMContentLoaded', loadStatus);
