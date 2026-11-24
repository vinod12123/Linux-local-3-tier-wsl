const services = {
  frontend: 'Stops Nginx, making the payment site unavailable.',
  backend: 'Stops the API; Nginx produces a real 502 response.',
  database: 'Stops PostgreSQL; API operations produce real 503 errors.'
};

const money = (cents, currency) => new Intl.NumberFormat(undefined, { style: 'currency', currency }).format(cents / 100);
const escapeHTML = (value) => String(value).replace(/[&<>'"]/g, c => ({'&':'&amp;','<':'&lt;','>':'&gt;',"'":'&#39;','"':'&quot;'}[c]));

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
  if (!response.ok) throw new Error(body.error || `Request failed with HTTP ${response.status}`);
  return body;
}

async function loadPayments() {
  const tbody = document.querySelector('#payments');
  try {
    const rows = await jsonRequest('/api/payments');
    tbody.innerHTML = rows.length ? rows.map(p => `<tr><td>${new Date(p.createdAt).toLocaleString()}</td><td>${escapeHTML(p.customerName)}</td><td>${money(p.amountCents,p.currency)}</td><td class="status-approved">${escapeHTML(p.status)}</td><td class="mono">${escapeHTML(p.id.slice(0,8))}</td></tr>`).join('') : '<tr><td colspan="5" class="empty">No payments yet. Create the first real database record above.</td></tr>';
  } catch (error) {
    tbody.innerHTML = `<tr><td colspan="5" class="empty">Data unavailable: ${escapeHTML(error.message)}</td></tr>`;
    showBanner(`Payment data could not be loaded: ${error.message}`);
  }
}

async function loadStatus() {
  const list = document.querySelector('#service-status');
  try {
    const states = await jsonRequest('/lab-api/status');
    let allUp = true;
    list.innerHTML = Object.keys(services).map(name => {
      const state = states[name] || {};
      allUp = allUp && Boolean(state.running);
      return `<div class="service-item"><div><span class="dot ${state.running?'up':''}"></span><span class="service-name">${name}</span></div><span class="service-detail">${escapeHTML(state.status||'unknown')} · ${escapeHTML(state.health||'unknown')} · exit ${state.exitCode??'-'}</span></div>`;
    }).join('');
    const overall = document.querySelector('#overall-status');
    overall.textContent = allUp ? 'All tiers running' : 'Service disruption detected';
    overall.className = `badge ${allUp ? 'healthy' : 'unhealthy'}`;
  } catch (error) {
    list.innerHTML = `<div class="empty">Controller unavailable: ${escapeHTML(error.message)}</div>`;
  }
}

async function runAction(service, action, button) {
  const label = action === 'restore' ? 'restore' : `${action} (cause a real outage in)`;
  if (!window.confirm(`Do you want to ${label} the ${service} tier?`)) return;
  button.disabled = true;
  try {
    await jsonRequest(`/lab-api/services/${service}/${action}`, { method: 'POST' });
    showBanner(`${service} ${action} request accepted. Watch ASTRA and the live state.`, action === 'restore' ? 'success' : 'error');
    setTimeout(() => { loadStatus(); loadPayments(); }, 1300);
  } catch (error) {
    showBanner(`${service} ${action} failed: ${error.message}`);
  } finally { button.disabled = false; }
}

document.querySelector('#simulation-grid').innerHTML = Object.entries(services).map(([name, description]) => `<article class="simulation-card"><h4>${name}</h4><p>${description}</p><div class="button-row"><button class="danger" data-service="${name}" data-action="crash">Crash</button><button class="warning" data-service="${name}" data-action="stop">Stop</button><button class="restore" data-service="${name}" data-action="restore">Restore</button></div></article>`).join('');
document.querySelector('#simulation-grid').addEventListener('click', event => {
  const button = event.target.closest('button[data-service]');
  if (button) runAction(button.dataset.service, button.dataset.action, button);
});
document.querySelector('#payment-form').addEventListener('submit', async event => {
  event.preventDefault();
  const button = event.currentTarget.querySelector('button');
  button.disabled = true;
  try {
    const result = await jsonRequest('/api/payments', { method:'POST', headers:{'Content-Type':'application/json'}, body: JSON.stringify({customerName:document.querySelector('#customer').value,amount:document.querySelector('#amount').value,currency:document.querySelector('#currency').value}) });
    showBanner(`Payment ${result.id.slice(0,8)} was stored in PostgreSQL.`, 'success');
    event.currentTarget.reset();
    await loadPayments();
  } catch (error) { showBanner(`Payment failed: ${error.message}`); }
  finally { button.disabled = false; }
});
document.querySelector('#refresh-payments').addEventListener('click', loadPayments);
document.querySelector('#refresh-status').addEventListener('click', loadStatus);
loadPayments(); loadStatus(); setInterval(loadStatus, 5000);

