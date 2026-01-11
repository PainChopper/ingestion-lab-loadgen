class LoadgenUI {
    constructor() {
        this.statusEl = document.getElementById('status');
        this.normalRadio = document.getElementById('normal');
        this.burstRadio = document.getElementById('burst');
        this.pulseSlider = document.getElementById('pulse');
        this.pulseValue = document.getElementById('pulseValue');
        this.quitBtn = document.getElementById('quitBtn');
        
        this.pulseDebounceTimer = null;
        
        this.initEventListeners();
        this.loadStatus();
    }
    
    initEventListeners() {
        this.normalRadio.addEventListener('change', () => this.handleModeChange('normal'));
        this.burstRadio.addEventListener('change', () => this.handleModeChange('burst'));
        this.pulseSlider.addEventListener('input', () => this.handlePulseChange());
        this.quitBtn.addEventListener('click', () => this.handleQuit());
    }
    
    async loadStatus() {
        try {
            const response = await fetch('/status');
            if (!response.ok) {
                throw new Error(`HTTP ${response.status}: ${response.statusText}`);
            }
            const status = await response.json();
            
            this.normalRadio.checked = status.mode === 'normal';
            this.burstRadio.checked = status.mode === 'burst';
            
            const pulseMs = this.parsePulseString(status.pulse);
            this.pulseSlider.value = pulseMs;
            this.pulseValue.textContent = status.pulse;
            
            this.showStatus('Connected to loadgen', 'success');
        } catch (error) {
            this.showStatus(`Failed to load status: ${error.message}`, 'error');
        }
    }
    
    parsePulseString(pulseStr) {
        const match = pulseStr.match(/^(\d+)ms$/);
        return match ? parseInt(match[1]) : 100;
    }
    
    handleModeChange(mode) {
        this.sendCommand('mode', mode);
    }
    
    handlePulseChange() {
        const value = this.pulseSlider.value;
        this.pulseValue.textContent = `${value}ms`;
        
        if (this.pulseDebounceTimer) {
            clearTimeout(this.pulseDebounceTimer);
        }
        
        this.pulseDebounceTimer = setTimeout(() => {
            this.sendCommand('pulse', `${value}ms`);
        }, 700);
    }
    
    handleQuit() {
        if (confirm('Are you sure you want to quit the loadgen?')) {
            this.sendCommand('quit', '');
        }
    }
    
    async sendCommand(action, value) {
        try {
            const response = await fetch('/control', {
                method: 'POST',
                headers: {
                    'Content-Type': 'application/json',
                },
                body: JSON.stringify({ action, value }),
            });
            
            if (!response.ok) {
                throw new Error(`HTTP ${response.status}: ${response.statusText}`);
            }
            
            this.showStatus(`Command sent: ${action} = ${value}`, 'success');
            
            if (action !== 'quit') {
                setTimeout(() => this.loadStatus(), 500);
            }
        } catch (error) {
            this.showStatus(`Failed to send command: ${error.message}`, 'error');
        }
    }
    
    showStatus(message, type) {
        this.statusEl.textContent = message;
        this.statusEl.className = `status ${type}`;
        this.statusEl.style.display = 'block';
        
        if (type === 'success') {
            setTimeout(() => {
                this.statusEl.style.display = 'none';
            }, 3000);
        }
    }
}

document.addEventListener('DOMContentLoaded', () => {
    new LoadgenUI();
});
