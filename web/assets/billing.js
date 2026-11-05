import { request } from './api.js';
import { formatTime, h } from './dom.js';

const money = (kopecks) => `${(kopecks / 100).toLocaleString('ru-RU', { minimumFractionDigits: 2 })} ₽`;
let page = 1;
let journalPage = 1;
let selected = null;
let pending = null;
let generation = 0;
let busy = false;
let notice = '';
let ratesLoaded = false;
let requestFailed = false;
const box = () => document.getElementById('billing');

// Начисления публичные: токен PVZ в другой сервис не отправляется.
export function initBilling() {
  box().replaceChildren(
    h('p', { id: 'billing-rates', class: 'muted' }, 'Загрузка ставок…'),
    h('p', { id: 'billing-notice', role: 'status' }),
    h('div', { class: 'buttons' }, h('button', { class: 'button', type: 'button', onclick: () => refresh() }, 'Обновить начисления')),
    h('div', { id: 'billing-balances' }),
    h('div', { id: 'billing-journal' }),
  );
  refresh();
}

// Опрос ограничен 20 секундами и прекращается при уходе со страницы.
export async function watchPayout(pvzID, receptionID) {
  selected = pvzID;
  journalPage = 1;
  pending = receptionID;
  const current = ++generation;
  const deadline = Date.now() + 20000;
  notice = 'Приёмка закрыта. Ждём начисление через outbox → Kafka → billing…';
  while (current === generation && Date.now() < deadline && pending) {
    await refresh();
    if (pending) await new Promise((resolve) => setTimeout(resolve, 1000));
  }
  if (current === generation && pending) {
    notice = 'Начисление ещё не пришло. Приёмка сохранена; обновите начисления позже.';
    renderNotice();
  }
}

window.addEventListener('pagehide', () => { generation++; });

function renderNotice() {
  document.getElementById('billing-notice').textContent = notice;
}

async function refresh() {
  if (busy) return;
  busy = true;
  if (requestFailed) {
    notice = pending ? 'Ждём начисление через Kafka…' : '';
    requestFailed = false;
  }
  renderNotice();
  try {
    if (!ratesLoaded) {
      const rates = await request('GET', '/billing/rates');
      document.getElementById('billing-rates').textContent = `За один товар: ${rates.map((r) => `${r.product_type} — ${money(r.amount_kopecks)}`).join(', ')}.`;
      ratesLoaded = true;
    }
    const balances = await request('GET', `/billing/payouts?page=${page}&limit=10`);
    document.getElementById('billing-balances').replaceChildren(
      h('h3', null, 'Балансы ПВЗ'),
      balances.length ? h('ul', { class: 'billing-list' }, balances.map((b) => h('li', null,
        h('button', { class: 'text-button', type: 'button', onclick: () => { selected = b.pvz_id; journalPage = 1; refresh(); } }, `${b.city} · ${b.pvz_id.slice(0, 8)} · ${money(b.amount_kopecks)}`),
      ))) : h('p', { class: 'muted' }, 'На этой странице начислений пока нет.'),
      pager(page, balances.length, (next) => { page = next; refresh(); }),
    );
    if (selected) {
      const journal = await request('GET', `/billing/payouts/${selected}?page=${journalPage}&limit=30`);
      const arrived = journal.find((p) => p.reception_id === pending);
      if (arrived) {
        pending = null;
        notice = `Начислено ${money(arrived.amount_kopecks)}. От закрытия до начисления: ${latency(arrived)}.`;
      }
      document.getElementById('billing-journal').replaceChildren(
        h('h3', null, `Журнал ПВЗ ${selected.slice(0, 8)}`),
        journal.length ? h('ol', { class: 'billing-list' }, journal.map((p) => h('li', null,
          h('p', null, `${formatTime(new Date(p.processed_at))} · ${money(p.amount_kopecks)} · товаров: ${p.products} · доставка: ${latency(p)}`),
          h('p', { class: 'small muted' }, `Приёмка ${p.reception_id}`),
          h('p', { class: 'small muted' }, `Событие ${p.event_id}`),
        ))) : h('p', { class: 'muted' }, 'Начислений пока нет.'),
        pager(journalPage, journal.length, (next) => { journalPage = next; refresh(); }, 30),
      );
    }
  } catch (err) {
    notice = `Начисления временно недоступны: ${err.message}`;
    requestFailed = true;
  } finally {
    busy = false;
    renderNotice();
  }
}

function latency(payout) {
  return `${Math.max(0, new Date(payout.processed_at) - new Date(payout.closed_at)).toLocaleString('ru-RU')} мс`;
}

function pager(current, count, go, limit = 10) {
  return h('div', { class: 'pager' },
    h('button', { class: 'button', type: 'button', disabled: current === 1, onclick: () => go(current - 1) }, '← Назад'),
    h('span', { class: 'muted' }, `Страница ${current}`),
    h('button', { class: 'button', type: 'button', disabled: count < limit, onclick: () => go(current + 1) }, 'Дальше →'),
  );
}
