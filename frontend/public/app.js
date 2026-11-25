const tierCards = [...document.querySelectorAll('[data-tier]')];
const overall = document.querySelector('#overall-status');
const banner = document.querySelector('#banner');
const requestTable = document.querySelector('#request-results');
const paymentsTable = document.querySelector('#payments');
const batchStatus = document.querySelector('#batch-status');
const stopBatch = document.querySelector('#stop-batch');
const batchButtons = [...document.querySelectorAll('[data-batch]')];
const labHeaders = { 'X-Lab-Action': '1' };
const requests = {
  read: { label: 'Read payments', path: '/api/payments' },
  exception: { label: 'API exception', path: '/api/lab/exception', method: 'POST' },
  databaseError: { label: 'PostgreSQL query error', path: '/api/lab/database-error', method: 'POST' },
  slowQuery: { label: 'Slow PostgreSQL query', path: '/api/lab/slow-query', method: 'POST' }
};
const batchSizes = { read: 30, exception: 8, databaseError: 8 };
let actionPending = false;
let batchRunning = false;
let batchStopped = false;
let paymentRequest = 0;
let statusRequest = 0;
let lastState = '';

function show(message, error = false) {
  banner.textContent = message;
  banner.classList.toggle('error', error);
  banner.hidden = false;
}

async function request(path, { timeout = 8000, ...options } = {}) {
  const abort = new AbortController();
  const timer = setTimeout(() => abort.abort(), timeout);
  try {
    const response = await fetch(path, { cache: 'no-store', ...options, signal: abort.signal });
    const body = response.headers.get('content-type')?.includes('application/json')
      ? await response.json()
      : null;
    if (!response.ok) {
      const details = body?.sqlstate ? body.error + ' (SQLSTATE ' + body.sqlstate + ')' : body?.error;
      const error = new Error('HTTP ' + response.status + (details ? ' · ' + details : ''));
      error.status = response.status;
      throw error;
    }
    if (body === null) throw new Error('Unexpected non-JSON response from ' + path);
    return { body, status: response.status };
  } catch (error) {
    if (error.name === 'AbortError') throw new Error('Request timed out');
    throw error;
  } finally {
    clearTimeout(timer);
  }
}

function describeState(state) {
  if (!state || state.status === 'unavailable') return { text: 'Unknown', tone: '' };
  if (state.exitCode != null) return { text: 'Exited · ' + state.exitCode, tone: 'down' };
  if (state.running && state.health === 'healthy') return { text: 'Healthy', tone: 'ok' };
  if (state.running) return { text: state.health || 'Running', tone: 'down' };
  return { text: state.status || 'Unknown', tone: 'down' };
}

function renderStatus(states) {
  let healthy = 0;
  let unknown = false;
  for (const card of tierCards) {
    const state = states?.[card.dataset.tier];
    const badge = card.querySelector('.tier-state');
    const description = describeState(state);
    badge.textContent = description.text;
    badge.className = 'badge tier-state ' + description.tone;
    healthy += Number(description.tone === 'ok');
    const known = Boolean(state?.status && state.status !== 'unavailable');
    unknown ||= !known;
    for (const button of card.querySelectorAll('[data-action]')) {
      const restore = button.dataset.action === 'restore';
      button.disabled = actionPending || !known || (restore ? state.running : !state.running);
    }
  }
  overall.className = 'badge ' + (unknown ? '' : healthy === tierCards.length ? 'ok' : 'down');
  overall.textContent = unknown ? 'Docker state unavailable' : healthy + ' / ' + tierCards.length + ' tiers healthy';
}

async function loadStatus() {
  const current = ++statusRequest;
  try {
    const { body: states } = await request('/lab-api/status');
    if (current !== statusRequest) return;
    renderStatus(states);
    const signature = JSON.stringify(states);
    if (lastState && signature !== lastState) loadPayments();
    lastState = signature;
  } catch {
    if (current === statusRequest) renderStatus(null);
  }
}

async function pollStatus() {
  await loadStatus();
  setTimeout(pollStatus, 5000);
}

function row(table, cells) {
  const rowElement = table.insertRow();
  for (const text of cells) rowElement.insertCell().textContent = text;
  return rowElement;
}

function emptyRow(table, columns, text) {
  const cell = row(table, [text]).cells[0];
  cell.colSpan = columns;
  cell.className = 'empty';
}

function formatPaymentAmount(payment) {
  const amount = payment.amountCents / 100;
  try {
    return new Intl.NumberFormat(undefined, { style: 'currency', currency: payment.currency }).format(amount);
  } catch {
    return amount.toFixed(2) + ' ' + payment.currency;
  }
}

async function loadPayments() {
  const current = ++paymentRequest;
  try {
    const { body } = await request('/api/payments');
    if (!Array.isArray(body)) throw new Error('Unexpected payment response');
    if (current !== paymentRequest) return;
    paymentsTable.replaceChildren();
    if (body.length === 0) emptyRow(paymentsTable, 4, 'No payments recorded.');
    for (const payment of body) {
      row(paymentsTable, [
        new Date(payment.createdAt).toLocaleString(),
        payment.customerName,
        formatPaymentAmount(payment),
        payment.status
      ]);
    }
  } catch (error) {
    if (current !== paymentRequest) return;
    paymentsTable.replaceChildren();
    emptyRow(paymentsTable, 4, 'Payment history unavailable · ' + error.message);
  }
}

async function runAction(tier, action) {
  if (actionPending) return;
  if (action !== 'restore' && !window.confirm(action + ' the ' + tier + ' container? This will interrupt the lab.')) return;
  actionPending = true;
  for (const button of document.querySelectorAll('[data-action]')) button.disabled = true;
  show('Waiting for Docker to confirm ' + action + ' on ' + tier + '…');
  try {
    const { body } = await request('/lab-api/services/' + tier + '/' + action, {
      method: 'POST', headers: labHeaders, timeout: 48000
    });
    show('Docker confirmed ' + tier + ' · ' + describeState(body).text);
  } catch (error) {
    const recovery = tier === 'frontend' ? ' Check the recovery console for the actual state.' : '';
    show('Could not verify ' + action + ' on ' + tier + ' · ' + error.message + '.' + recovery, true);
  } finally {
    actionPending = false;
    await Promise.all([loadStatus(), loadPayments()]);
  }
}

function recordRequest(label, text, started, failed) {
  if (requestTable.rows[0]?.cells.length === 1) requestTable.deleteRow(0);
  row(requestTable, [new Date().toLocaleTimeString(), label, text, Math.round(performance.now() - started) + ' ms']);
  const latest = requestTable.rows[requestTable.rows.length - 1];
  if (failed) latest.cells[2].className = 'result-error';
  requestTable.prepend(latest);
  if (requestTable.rows.length > 30) requestTable.deleteRow(-1);
}

async function runRequest(name) {
  const { label, path, method = 'GET' } = requests[name];
  const started = performance.now();
  try {
    const { status } = await request(path, { method, headers: labHeaders });
    recordRequest(label, 'HTTP ' + status, started, false);
    return true;
  } catch (error) {
    recordRequest(label, error.message, started, true);
    return false;
  }
}

async function runBatch(name) {
  if (batchRunning) return;
  batchRunning = true;
  batchStopped = false;
  for (const button of batchButtons) button.disabled = true;
  stopBatch.disabled = false;
  let completed = 0;
  let successes = 0;
  const max = batchSizes[name];
  const progress = () => completed + ' / ' + max + ' completed · ' + successes + ' succeeded · ' + (completed - successes) + ' failed';
  batchStatus.textContent = progress();
  try {
    while (completed < max && !batchStopped) {
      if (await runRequest(name)) successes++;
      completed++;
      batchStatus.textContent = progress();
      if (completed < max && !batchStopped) await new Promise(resolve => setTimeout(resolve, 150));
    }
  } finally {
    batchStatus.textContent = (batchStopped ? 'Stopped · ' : 'Finished · ') + progress();
    batchRunning = false;
    stopBatch.disabled = true;
    for (const button of batchButtons) button.disabled = false;
  }
}

for (const card of tierCards) {
  for (const button of card.querySelectorAll('[data-action]')) {
    button.addEventListener('click', () => runAction(card.dataset.tier, button.dataset.action));
  }
}
for (const button of document.querySelectorAll('[data-request]')) {
  button.addEventListener('click', async () => {
    button.disabled = true;
    try {
      await runRequest(button.dataset.request);
    } finally {
      button.disabled = false;
    }
  });
}
for (const button of batchButtons) button.addEventListener('click', () => runBatch(button.dataset.batch));
stopBatch.addEventListener('click', () => {
  batchStopped = true;
  stopBatch.disabled = true;
});
document.querySelector('#scenario-filter').addEventListener('change', event => {
  document.querySelector('#availability').hidden = event.target.value === 'requests';
  document.querySelector('#traffic').hidden = event.target.value === 'availability';
});
for (const link of document.querySelectorAll('nav a')) {
  link.addEventListener('click', () => {
    document.querySelector('#scenario-filter').value = 'all';
    document.querySelector('#availability').hidden = false;
    document.querySelector('#traffic').hidden = false;
  });
}
document.querySelector('#payment-form').addEventListener('submit', async event => {
  event.preventDefault();
  const form = event.currentTarget;
  const button = form.querySelector('button');
  const feedback = document.querySelector('#payment-feedback');
  const started = performance.now();
  button.disabled = true;
  feedback.textContent = 'Saving payment…';
  try {
    const { body, status } = await request('/api/payments', {
      method: 'POST',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify(Object.fromEntries(new FormData(form)))
    });
    recordRequest('Save payment', 'HTTP ' + status, started, false);
    feedback.textContent = 'Payment ' + body.id.slice(0, 8) + ' saved to PostgreSQL.';
    form.reset();
    await loadPayments();
  } catch (error) {
    recordRequest('Save payment', error.message, started, true);
    feedback.textContent = 'Payment failed · ' + error.message;
  } finally {
    button.disabled = false;
  }
});
document.querySelector('#refresh-payments').addEventListener('click', loadPayments);
document.querySelector('#refresh-status').addEventListener('click', loadStatus);
loadPayments();
pollStatus();

