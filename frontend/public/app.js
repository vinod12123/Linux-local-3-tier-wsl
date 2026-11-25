const services = {
  frontend: 'Stops Nginx, making the payment site unavailable.',
  backend: 'Stops the API; Nginx produces a real 502 response.',
  database: 'Stops PostgreSQL; API operations produce real 503 errors.'
};

const money = (cents, currency) => new Intl.NumberFormat(undefined, { style: 'currency', currency }).format(cents / 100);
const escapeHTML = (value) => String(value).replace(/[&<>'"]/g, c => ({'&':'&amp;','<':'&lt;','>':'&gt;',"'":'&#39;','"':'&quot;'}[c]));

let paymentRequest = 0;
let previousTierState = '';

  if (!response.ok) {
    const status = `HTTP ${response.status}`;
    throw new Error(body.error && body.error !== status ? `${status}: ${body.error}` : status);
  }
function showBanner(message, kind = 'error') {
  const banner = document.querySelector('#banner');
  banner.textContent = message;
  banner.className = `banner ${kind}`;
  clearTimeout(showBanner.timer);
  showBanner.timer = setTimeout(() => banner.classList.add('hidden'), 7000);
}

async function jsonRequest(url, options) {
  const response = await fetch(url, options);
  let body;
  try { body = await response.json(); } catch { body = { error: `HTTP ${response.status}` }; }
  return body;
}

async function loadPayments(showError = false) {
  const tbody = document.querySelector('#payments');
  const request = ++paymentRequest;
  try {
    const rows = await jsonRequest('/api/payments', { cache: 'no-store' });
    if (request !== paymentRequest) return;
    tbody.innerHTML = rows.length ? rows.map(p => `<tr><td>${new Date(p.createdAt).toLocaleString()}</td><td>${escapeHTML(p.customerName)}</td><td>${money(p.amountCents,p.currency)}</td><td class="status-approved">${escapeHTML(p.status)}</td><td class="mono">${escapeHTML(p.id.slice(0,8))}</td></tr>`).join('') : '<tr><td colspan="5" class="empty">No payments yet. Create the first real database record above.</td></tr>';
  } catch (error) {
    if (request !== paymentRequest) return;
    tbody.innerHTML = `<tr><td colspan="5" class="empty">Data unavailable: ${escapeHTML(error.message)}</td></tr>`;
    if (showError) showBanner(`Payment data could not be loaded: ${error.message}`);
  }
}

async function loadStatus() {
  const list = document.querySelector('#service-status');
  try {
    const states = await jsonRequest('/lab-api/status', { cache: 'no-store' });
    let allUp = true;
    let unknown = false;
    list.innerHTML = Object.keys(services).map(name => {
      const state = states[name] || {};
      const healthy = state.running && state.health === 'healthy';
      allUp = allUp && healthy;
      unknown = unknown || !state.status || state.status === 'unavailable';
      return `<div class="service-item"><div><span class="dot ${healthy?'up':''}"></span><span class="service-name">${name}</span></div><span class="service-detail">${escapeHTML(formatState(state))}</span></div>`;
    }).join('');
    const overall = document.querySelector('#overall-status');
    overall.textContent = unknown ? 'Docker state unavailable' : allUp ? 'All tier checks passing' : 'Tier stopped or not ready';
    overall.className = `badge ${unknown ? 'neutral' : allUp ? 'healthy' : 'unhealthy'}`;
    const tierState = Object.keys(services).map(name => formatState(states[name] || {})).join('|');
    if (previousTierState && tierState !== previousTierState) loadPayments();
    previousTierState = tierState;
  } catch (error) {
    list.innerHTML = `<div class="empty">Controller unavailable: ${escapeHTML(error.message)}</div>`;
    const overall = document.querySelector('#overall-status');
    overall.textContent = 'Docker state unavailable';
    overall.className = 'badge neutral';
  }
}

function formatState(state) {
  if (state.exitCode != null) return `${state.status} · exit ${state.exitCode}`;
  if (state.running) return `${state.status} · ${state.health}`;
  return state.status || 'unknown';
}

async function runAction(service, action, button) {
  const label = action === 'restore' ? 'restore' : `${action} (cause a real outage in)`;
  if (!window.confirm(`Do you want to ${label} the ${service} tier?`)) return;
  button.disabled = true;
  try {
    const state = await jsonRequest(`/lab-api/services/${service}/${action}`, {
      method: 'POST',
      headers: { 'X-Lab-Action': '1' }
    });
    showBanner(`Docker confirmed ${service}: ${formatState(state)}.`, action === 'restore' ? 'success' : 'error');
  } catch (error) {
    const recovery = service === 'frontend' && action !== 'restore' ? ' Check the recovery console for its live state.' : '';
    showBanner(`Could not verify ${service} ${action}: ${error.message}.${recovery}`);
  } finally {
    await Promise.all([loadStatus(), loadPayments()]);
    button.disabled = false;
  }
}

document.querySelector('#simulation-grid').innerHTML = Object.entries(services).map(([name, description]) => `<article class="simulation-card"><h4>${name}</h4><p>${description}</p><div class="button-row"><button class="danger" data-service="${name}" data-action="crash">Crash</button><button class="warning" data-service="${name}" data-action="stop">Stop</button><button class="restore" data-service="${name}" data-action="restore">Restore</button></div></article>`).join('');
document.querySelector('#simulation-grid').addEventListener('click', event => {
  const button = event.target.closest('button[data-service]');
  if (button) runAction(button.dataset.service, button.dataset.action, button);
});
document.querySelector('#payment-form').addEventListener('submit', async event => {
  event.preventDefault();
  const form = event.currentTarget;
  const button = form.querySelector('button');
  button.disabled = true;
  try {
    const result = await jsonRequest('/api/payments', { method:'POST', headers:{'Content-Type':'application/json'}, body: JSON.stringify({customerName:document.querySelector('#customer').value,amount:document.querySelector('#amount').value,currency:document.querySelector('#currency').value}) });
    showBanner(`Payment ${result.id.slice(0,8)} was stored in PostgreSQL.`, 'success');
    form.reset();
    await loadPayments();
  } catch (error) { showBanner(`Payment failed: ${error.message}`); }
  finally { button.disabled = false; }
});
document.querySelector('#refresh-payments').addEventListener('click', () => loadPayments(true));
document.querySelector('#refresh-status').addEventListener('click', loadStatus);
loadPayments(); loadStatus(); setInterval(loadStatus, 5000);

